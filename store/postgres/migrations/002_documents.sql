CREATE TABLE IF NOT EXISTS weave_documents (
    id              TEXT PRIMARY KEY,
    collection_id   TEXT NOT NULL REFERENCES weave_collections(id) ON DELETE CASCADE,
    tenant_id       TEXT NOT NULL,
    title           TEXT,
    source          TEXT,
    source_type     TEXT,
    content_hash    TEXT NOT NULL,
    content_length  INT NOT NULL DEFAULT 0,
    chunk_count     INT NOT NULL DEFAULT 0,
    metadata        JSONB NOT NULL DEFAULT '{}',
    state           TEXT NOT NULL DEFAULT 'pending',
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE(collection_id, content_hash)
);

CREATE INDEX IF NOT EXISTS idx_weave_documents_collection ON weave_documents (collection_id, state);
CREATE INDEX IF NOT EXISTS idx_weave_documents_tenant ON weave_documents (tenant_id);
