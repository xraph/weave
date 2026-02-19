CREATE TABLE IF NOT EXISTS weave_collections (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT,
    tenant_id       TEXT NOT NULL,
    app_id          TEXT NOT NULL,
    embedding_model TEXT NOT NULL DEFAULT 'text-embedding-3-small',
    embedding_dims  INT NOT NULL DEFAULT 1536,
    chunk_strategy  TEXT NOT NULL DEFAULT 'recursive',
    chunk_size      INT NOT NULL DEFAULT 512,
    chunk_overlap   INT NOT NULL DEFAULT 50,
    metadata        JSONB NOT NULL DEFAULT '{}',
    document_count  BIGINT NOT NULL DEFAULT 0,
    chunk_count     BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_weave_collections_tenant ON weave_collections (tenant_id);
