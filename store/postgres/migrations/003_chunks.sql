CREATE TABLE IF NOT EXISTS weave_chunks (
    id              TEXT PRIMARY KEY,
    document_id     TEXT NOT NULL REFERENCES weave_documents(id) ON DELETE CASCADE,
    collection_id   TEXT NOT NULL REFERENCES weave_collections(id) ON DELETE CASCADE,
    tenant_id       TEXT NOT NULL,
    content         TEXT NOT NULL,
    index           INT NOT NULL,
    start_offset    INT NOT NULL DEFAULT 0,
    end_offset      INT NOT NULL DEFAULT 0,
    token_count     INT NOT NULL DEFAULT 0,
    metadata        JSONB NOT NULL DEFAULT '{}',
    parent_id       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_weave_chunks_document ON weave_chunks (document_id, index);
CREATE INDEX IF NOT EXISTS idx_weave_chunks_collection ON weave_chunks (collection_id);
CREATE INDEX IF NOT EXISTS idx_weave_chunks_tenant ON weave_chunks (tenant_id);
