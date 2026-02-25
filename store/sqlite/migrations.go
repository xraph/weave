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
	)
}
