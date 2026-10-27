package sqlite

import (
	"context"

	"github.com/xraph/grove/migrate"
)

// Migrations is the grove migration group for the Weave SQLite store.
var Migrations = migrate.NewGroup("weave")

func init() {
	Migrations.MustRegister(
		&migrate.Migration{
			Name:    "create_weave_collections",
			Version: "20240101000000",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS weave_collections (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT,
    tenant_id       TEXT NOT NULL,
    app_id          TEXT NOT NULL,
    embedding_model TEXT NOT NULL DEFAULT 'text-embedding-3-small',
    embedding_dims  INTEGER NOT NULL DEFAULT 1536,
    chunk_strategy  TEXT NOT NULL DEFAULT 'recursive',
    chunk_size      INTEGER NOT NULL DEFAULT 512,
    chunk_overlap   INTEGER NOT NULL DEFAULT 50,
    metadata        TEXT NOT NULL DEFAULT '{}',
    document_count  INTEGER NOT NULL DEFAULT 0,
    chunk_count     INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),

    UNIQUE(tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_weave_collections_tenant ON weave_collections (tenant_id);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS weave_collections;`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "create_weave_documents",
			Version: "20240101000001",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS weave_documents (
    id              TEXT PRIMARY KEY,
    collection_id   TEXT NOT NULL REFERENCES weave_collections(id) ON DELETE CASCADE,
    tenant_id       TEXT NOT NULL,
    title           TEXT,
    source          TEXT,
    source_type     TEXT,
    content_hash    TEXT NOT NULL,
    content_length  INTEGER NOT NULL DEFAULT 0,
    chunk_count     INTEGER NOT NULL DEFAULT 0,
    metadata        TEXT NOT NULL DEFAULT '{}',
    state           TEXT NOT NULL DEFAULT 'pending',
    error           TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),

    UNIQUE(collection_id, content_hash)
);

CREATE INDEX IF NOT EXISTS idx_weave_documents_collection ON weave_documents (collection_id, state);
CREATE INDEX IF NOT EXISTS idx_weave_documents_tenant ON weave_documents (tenant_id);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS weave_documents;`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "create_weave_chunks",
			Version: "20240101000002",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
CREATE TABLE IF NOT EXISTS weave_chunks (
    id              TEXT PRIMARY KEY,
    document_id     TEXT NOT NULL REFERENCES weave_documents(id) ON DELETE CASCADE,
    collection_id   TEXT NOT NULL REFERENCES weave_collections(id) ON DELETE CASCADE,
    tenant_id       TEXT NOT NULL,
    content         TEXT NOT NULL,
    "index"         INTEGER NOT NULL,
    start_offset    INTEGER NOT NULL DEFAULT 0,
    end_offset      INTEGER NOT NULL DEFAULT 0,
    token_count     INTEGER NOT NULL DEFAULT 0,
    metadata        TEXT NOT NULL DEFAULT '{}',
    parent_id       TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_weave_chunks_document ON weave_chunks (document_id, "index");
CREATE INDEX IF NOT EXISTS idx_weave_chunks_collection ON weave_chunks (collection_id);
CREATE INDEX IF NOT EXISTS idx_weave_chunks_tenant ON weave_chunks (tenant_id);
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `DROP TABLE IF EXISTS weave_chunks;`)
				return err
			},
		},
		&migrate.Migration{
			Name:    "add_weave_documents_source_key",
			Version: "20261024000100",
			Up: func(ctx context.Context, exec migrate.Executor) error {
				// SQLite has no ADD COLUMN IF NOT EXISTS, so the column is
				// looked up first and the migration stays safe to rerun.
				has, err := hasColumn(ctx, exec, "weave_documents", "source_key")
				if err != nil {
					return err
				}
				if !has {
					if _, err = exec.Exec(ctx, `ALTER TABLE weave_documents ADD COLUMN source_key TEXT NOT NULL DEFAULT ''`); err != nil {
						return err
					}
				}
				_, err = exec.Exec(ctx, `
CREATE UNIQUE INDEX IF NOT EXISTS idx_weave_documents_source_ready
    ON weave_documents (collection_id, source_key) WHERE source_key <> '' AND state = 'ready';
CREATE INDEX IF NOT EXISTS idx_weave_documents_source
    ON weave_documents (tenant_id, collection_id, source_key) WHERE source_key <> '';
`)
				return err
			},
			Down: func(ctx context.Context, exec migrate.Executor) error {
				_, err := exec.Exec(ctx, `
DROP INDEX IF EXISTS idx_weave_documents_source;
DROP INDEX IF EXISTS idx_weave_documents_source_ready;
ALTER TABLE weave_documents DROP COLUMN source_key;
`)
				return err
			},
		},
	)
}

// hasColumn reports whether table has a column named column.
func hasColumn(ctx context.Context, exec migrate.Executor, table, column string) (bool, error) {
	rows, err := exec.Query(ctx, `SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	var n int
	if rows.Next() {
		err = rows.Scan(&n)
		if err != nil {
			return false, err
		}
	}
	return n > 0, rows.Err()
}
