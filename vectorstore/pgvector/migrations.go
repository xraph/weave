package pgvector

import (
	"context"
	"fmt"

	"github.com/xraph/grove"
	"github.com/xraph/grove/driver"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"
	"github.com/xraph/grove/migrate"

	"github.com/xraph/weave"
	"github.com/xraph/weave/internal/migrateretry"
)

// GroupName is the grove migration group that owns the vector table. It is
// separate from the metadata store's "weave" group, so a host that never
// turns pgvector on never needs the vector extension.
const GroupName = "weave_pgvector"

// MaxIndexedDimensions is the largest vector an HNSW index accepts.
const MaxIndexedDimensions = 2000

// defaultTable is the table Migrations creates and New uses by default.
const defaultTable = "weave_vectors"

// Migrations returns the tracked group that creates the vector extension,
// the weave_vectors table with an embedding vector(dimensions) column, and
// its indexes. dimensions outside 1..MaxIndexedDimensions is
// ErrInvalidArgument. The group depends on no other group, so a host runs it
// on its own or adds it to its own orchestrator run.
//
// The migration is recorded once, so it never runs again with another
// dimensions. When weave_vectors already exists, made by hand or by an
// earlier run, the migration fails with ErrDimensionMismatch if its
// embedding column has another size, and nothing is recorded. Call
// Store.CheckDimensions after a run that applied nothing to catch a changed
// setting.
func Migrations(dimensions int) (*migrate.Group, error) {
	if dimensions < 1 || dimensions > MaxIndexedDimensions {
		return nil, fmt.Errorf("%w: pgvector dimensions %d, want 1 to %d", weave.ErrInvalidArgument, dimensions, MaxIndexedDimensions)
	}
	g := migrate.NewGroup(GroupName)
	if err := g.Register(&migrate.Migration{
		Name:    "create_weave_vectors",
		Version: "20261024000300",
		Up: func(ctx context.Context, exec migrate.Executor) error {
			if _, err := exec.Exec(ctx, fmt.Sprintf(`
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE IF NOT EXISTS weave_vectors (
    id        TEXT PRIMARY KEY,
    embedding vector(%d) NOT NULL,
    content   TEXT NOT NULL DEFAULT '',
    metadata  JSONB NOT NULL DEFAULT '{}'
);`, dimensions)); err != nil {
				return err
			}
			if err := checkTable(ctx, exec, defaultTable, dimensions); err != nil {
				return err
			}
			_, err := exec.Exec(ctx, `
CREATE INDEX IF NOT EXISTS idx_weave_vectors_embedding ON weave_vectors USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS idx_weave_vectors_tenant_collection ON weave_vectors ((metadata->>'tenant_id'), (metadata->>'collection_id'));
CREATE INDEX IF NOT EXISTS idx_weave_vectors_document ON weave_vectors ((metadata->>'document_id'));
`)
			return err
		},
		Down: func(ctx context.Context, exec migrate.Executor) error {
			// The extension stays: something else on the database may use it.
			_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS weave_vectors`)
			return err
		},
	}); err != nil {
		return nil, fmt.Errorf("weave: register pgvector migrations: %w", err)
	}
	return g, nil
}

// Migrate runs Migrations(dimensions) through grove's orchestrator, then
// checks the live table's dimensions (ErrDimensionMismatch). Replicas
// booting together on a database without grove's tracking tables race on
// creating them, so a lost race is retried a few times, as the postgres
// metadata store's Migrate does.
func Migrate(ctx context.Context, db *grove.DB, dimensions int) error {
	g, err := Migrations(dimensions)
	if err != nil {
		return err
	}
	pg := pgdriver.Unwrap(db)
	if err := migrateretry.Run(ctx, pgmigrate.New(pg), g); err != nil {
		return fmt.Errorf("%w: %w", weave.ErrMigrationFailed, err)
	}
	return New(db, WithDimensions(dimensions)).CheckDimensions(ctx)
}

// queryer runs a row-returning statement: a pool, a transaction or a
// migration executor.
type queryer interface {
	Query(ctx context.Context, query string, args ...any) (driver.Rows, error)
}

// checkTable compares table's embedding column with dimensions. A missing
// table wraps ErrNoVectorStore; another size is ErrDimensionMismatch naming
// both.
func checkTable(ctx context.Context, q queryer, table string, dimensions int) error {
	rows, err := q.Query(ctx, `
SELECT to_regclass($1) IS NOT NULL,
       (SELECT atttypmod FROM pg_attribute
         WHERE attrelid = to_regclass($1) AND attname = 'embedding' AND NOT attisdropped)`,
		quoteTable(table))
	if err != nil {
		return fmt.Errorf("weave: read %s dimensions: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	var exists bool
	var typmod *int
	if rows.Next() {
		if err := rows.Scan(&exists, &typmod); err != nil {
			return fmt.Errorf("weave: read %s dimensions: %w", table, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("weave: read %s dimensions: %w", table, err)
	}

	switch {
	case !exists:
		return fmt.Errorf("%w: table %s does not exist; run pgvector.Migrate", weave.ErrNoVectorStore, table)
	case typmod == nil:
		return fmt.Errorf("%w: table %s has no embedding column", weave.ErrDimensionMismatch, table)
	case *typmod < 1:
		return fmt.Errorf("%w: %s.embedding has no fixed dimensions, want %d", weave.ErrDimensionMismatch, table, dimensions)
	case *typmod != dimensions:
		return fmt.Errorf("%w: %s.embedding has %d dimensions, want %d", weave.ErrDimensionMismatch, table, *typmod, dimensions)
	}
	return nil
}
