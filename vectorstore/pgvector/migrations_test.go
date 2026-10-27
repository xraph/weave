package pgvector_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"

	"github.com/xraph/weave"
	"github.com/xraph/weave/vectorstore/pgvector"
)

func TestMigrationsRejectsDimensions(t *testing.T) {
	for _, dims := range []int{0, -1, pgvector.MaxIndexedDimensions + 1} {
		if _, err := pgvector.Migrations(dims); !errors.Is(err, weave.ErrInvalidArgument) {
			t.Errorf("Migrations(%d): %v, want ErrInvalidArgument", dims, err)
		}
	}

	g, err := pgvector.Migrations(1536)
	if err != nil {
		t.Fatalf("Migrations(1536): %v", err)
	}
	if g.Name() != "weave_pgvector" || g.Name() != pgvector.GroupName {
		t.Errorf("group name %q, want weave_pgvector", g.Name())
	}
	ms := g.Migrations()
	if len(ms) != 1 || ms[0].Version != "20261024000300" || ms[0].Name != "create_weave_vectors" {
		t.Fatalf("migrations %+v, want one create_weave_vectors at 20261024000300", ms)
	}
	if deps := g.DependsOnGroups(); len(deps) != 0 {
		t.Errorf("depends on %v, want no group", deps)
	}
	if ms[0].Down == nil {
		t.Error("migration has no Down")
	}
}

func TestMigrateCreatesTableAndIndexes(t *testing.T) {
	ctx, db := openVectorDB(t)
	pg := pgdriver.Unwrap(db)

	if err := pgvector.Migrate(ctx, db, 8); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var reg *string
	if err := pg.QueryRow(ctx, `SELECT to_regclass('weave_vectors')::text`).Scan(&reg); err != nil || reg == nil {
		t.Fatalf("to_regclass(weave_vectors) = %v, %v; want the table", reg, err)
	}
	if got := embeddingTypmod(ctx, t, pg, "weave_vectors"); got != 8 {
		t.Errorf("embedding atttypmod = %d, want 8", got)
	}

	indexes := map[string]string{}
	rows, err := pg.Query(ctx, `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'weave_vectors'`)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		indexes[name] = def
	}
	_ = rows.Close()
	if def := indexes["idx_weave_vectors_embedding"]; !strings.Contains(def, "hnsw") || !strings.Contains(def, "vector_cosine_ops") {
		t.Errorf("idx_weave_vectors_embedding = %q, want an hnsw index with vector_cosine_ops", def)
	}
	for _, name := range []string{"idx_weave_vectors_tenant_collection", "idx_weave_vectors_document"} {
		if _, ok := indexes[name]; !ok {
			t.Errorf("index %s missing; have %v", name, indexes)
		}
	}

	if got := appliedVersions(ctx, t, pg); len(got) != 1 || got[0] != "20261024000300" {
		t.Errorf("applied weave_pgvector versions = %v, want [20261024000300]", got)
	}
	if err := pgvector.Migrate(ctx, db, 8); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := appliedVersions(ctx, t, pg); len(got) != 1 {
		t.Errorf("applied weave_pgvector versions after a second run = %v, want one", got)
	}
}

func TestMigrateRefusesOtherDimensions(t *testing.T) {
	t.Run("changed after the table exists", func(t *testing.T) {
		ctx, db := openVectorDB(t)
		if err := pgvector.Migrate(ctx, db, 8); err != nil {
			t.Fatalf("migrate 8: %v", err)
		}
		err := pgvector.Migrate(ctx, db, 16)
		if !errors.Is(err, weave.ErrDimensionMismatch) {
			t.Fatalf("migrate 16: %v, want ErrDimensionMismatch", err)
		}
		if !strings.Contains(err.Error(), "8") || !strings.Contains(err.Error(), "16") {
			t.Errorf("migrate 16: %q, want both 8 and 16 named", err)
		}
	})

	t.Run("hand-made table of another size", func(t *testing.T) {
		ctx, db := openVectorDB(t)
		pg := pgdriver.Unwrap(db)
		if _, err := pg.Exec(ctx, `CREATE TABLE weave_vectors (id TEXT PRIMARY KEY, embedding vector(4), content TEXT, metadata JSONB)`); err != nil {
			t.Fatalf("create hand-made table: %v", err)
		}
		err := pgvector.Migrate(ctx, db, 8)
		if !errors.Is(err, weave.ErrDimensionMismatch) {
			t.Fatalf("migrate 8 over vector(4): %v, want ErrDimensionMismatch", err)
		}
		if !strings.Contains(err.Error(), "4") || !strings.Contains(err.Error(), "8") {
			t.Errorf("migrate 8 over vector(4): %q, want both 4 and 8 named", err)
		}
		if got := appliedVersions(ctx, t, pg); len(got) != 0 {
			t.Errorf("applied weave_pgvector versions = %v, want none recorded", got)
		}
	})
}

// TestMigrateConcurrentFirstBoot starts eight replicas on one empty schema at
// once, each with its own pool. Grove creates its tracking tables before it
// takes the migration lock, so they race on that CREATE; every Migrate must
// still succeed and the version be recorded once.
func TestMigrateConcurrentFirstBoot(t *testing.T) {
	ctx, scoped := vectorSchema(t)

	const replicas = 8
	dbs := make([]*grove.DB, replicas)
	for i := range dbs {
		dbs[i] = openDB(ctx, t, scoped)
	}

	errs := make([]error, replicas)
	var wg sync.WaitGroup
	for i, db := range dbs {
		wg.Go(func() { errs[i] = pgvector.Migrate(ctx, db, 8) })
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d migrate: %v", i, err)
		}
	}
	if got := appliedVersions(ctx, t, pgdriver.Unwrap(dbs[0])); len(got) != 1 {
		t.Errorf("applied weave_pgvector versions = %v, want one", got)
	}
}

func TestCheckDimensions(t *testing.T) {
	ctx, db := openVectorDB(t)
	if err := pgvector.New(db, pgvector.WithDimensions(8)).CheckDimensions(ctx); !errors.Is(err, weave.ErrNoVectorStore) {
		t.Errorf("empty schema: %v, want ErrNoVectorStore", err)
	}

	if err := pgvector.Migrate(ctx, db, 8); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := pgvector.New(db, pgvector.WithDimensions(8)).CheckDimensions(ctx); err != nil {
		t.Errorf("WithDimensions(8): %v, want nil", err)
	}
	err := pgvector.New(db, pgvector.WithDimensions(16)).CheckDimensions(ctx)
	if !errors.Is(err, weave.ErrDimensionMismatch) {
		t.Fatalf("WithDimensions(16): %v, want ErrDimensionMismatch", err)
	}
	if !strings.Contains(err.Error(), "8") || !strings.Contains(err.Error(), "16") {
		t.Errorf("WithDimensions(16): %q, want both 8 and 16 named", err)
	}
	if err := pgvector.New(db).CheckDimensions(ctx); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("no WithDimensions: %v, want ErrInvalidArgument", err)
	}
}

// appliedVersions returns the weave_pgvector versions grove recorded.
func appliedVersions(ctx context.Context, t *testing.T, pg *pgdriver.PgDB) []string {
	t.Helper()
	applied, err := pgmigrate.New(pg).ListApplied(ctx)
	if err != nil {
		t.Fatalf("list applied: %v", err)
	}
	var got []string
	for _, a := range applied {
		if a.Group == pgvector.GroupName {
			got = append(got, a.Version)
		}
	}
	return got
}

// embeddingTypmod reads the declared size of table's embedding column.
func embeddingTypmod(ctx context.Context, t *testing.T, pg *pgdriver.PgDB, table string) int {
	t.Helper()
	var mod int
	if err := pg.QueryRow(ctx, `SELECT atttypmod FROM pg_attribute WHERE attrelid = to_regclass($1) AND attname = 'embedding'`, table).Scan(&mod); err != nil {
		t.Fatalf("read %s.embedding typmod: %v", table, err)
	}
	return mod
}
