package postgres

import (
	"time"

	"github.com/uptrace/bun"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

// ──────────────────────────────────────────────────
// Collection model
// ──────────────────────────────────────────────────

type collectionModel struct {
	bun.BaseModel `bun:"table:weave_collections"`

	ID             string            `bun:"id,pk"`
	Name           string            `bun:"name,notnull"`
	Description    string            `bun:"description"`
	TenantID       string            `bun:"tenant_id,notnull"`
	AppID          string            `bun:"app_id,notnull"`
	EmbeddingModel string            `bun:"embedding_model,notnull"`
	EmbeddingDims  int               `bun:"embedding_dims,notnull"`
	ChunkStrategy  string            `bun:"chunk_strategy,notnull"`
	ChunkSize      int               `bun:"chunk_size,notnull"`
	ChunkOverlap   int               `bun:"chunk_overlap,notnull"`
	Metadata       map[string]string `bun:"metadata,type:jsonb"`
	DocumentCount  int64             `bun:"document_count,notnull"`
	ChunkCount     int64             `bun:"chunk_count,notnull"`
	CreatedAt      time.Time         `bun:"created_at,notnull"`
	UpdatedAt      time.Time         `bun:"updated_at,notnull"`
}

func collectionToModel(c *collection.Collection) *collectionModel {
	return &collectionModel{
		ID:             c.ID.String(),
		Name:           c.Name,
		Description:    c.Description,
		TenantID:       c.TenantID,
		AppID:          c.AppID,
		EmbeddingModel: c.EmbeddingModel,
		EmbeddingDims:  c.EmbeddingDims,
		ChunkStrategy:  c.ChunkStrategy,
		ChunkSize:      c.ChunkSize,
		ChunkOverlap:   c.ChunkOverlap,
		Metadata:       c.Metadata,
		DocumentCount:  c.DocumentCount,
		ChunkCount:     c.ChunkCount,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}

func collectionFromModel(m *collectionModel) *collection.Collection {
	colID, _ := id.ParseCollectionID(m.ID)
	return &collection.Collection{
		ID:             colID,
		Name:           m.Name,
		Description:    m.Description,
		TenantID:       m.TenantID,
		AppID:          m.AppID,
		EmbeddingModel: m.EmbeddingModel,
		EmbeddingDims:  m.EmbeddingDims,
		ChunkStrategy:  m.ChunkStrategy,
		ChunkSize:      m.ChunkSize,
		ChunkOverlap:   m.ChunkOverlap,
		Metadata:       m.Metadata,
		DocumentCount:  m.DocumentCount,
		ChunkCount:     m.ChunkCount,
	}
}

// ──────────────────────────────────────────────────
// Document model
// ──────────────────────────────────────────────────

type documentModel struct {
	bun.BaseModel `bun:"table:weave_documents"`

	ID            string            `bun:"id,pk"`
	CollectionID  string            `bun:"collection_id,notnull"`
	TenantID      string            `bun:"tenant_id,notnull"`
	Title         string            `bun:"title"`
	Source        string            `bun:"source"`
	SourceType    string            `bun:"source_type"`
	ContentHash   string            `bun:"content_hash,notnull"`
	ContentLength int               `bun:"content_length,notnull"`
	ChunkCount    int               `bun:"chunk_count,notnull"`
	Metadata      map[string]string `bun:"metadata,type:jsonb"`
	State         string            `bun:"state,notnull"`
	Error         string            `bun:"error"`
	CreatedAt     time.Time         `bun:"created_at,notnull"`
	UpdatedAt     time.Time         `bun:"updated_at,notnull"`
}

func documentToModel(d *document.Document) *documentModel {
	return &documentModel{
		ID:            d.ID.String(),
		CollectionID:  d.CollectionID.String(),
		TenantID:      d.TenantID,
		Title:         d.Title,
		Source:        d.Source,
		SourceType:    d.SourceType,
		ContentHash:   d.ContentHash,
		ContentLength: d.ContentLength,
		ChunkCount:    d.ChunkCount,
		Metadata:      d.Metadata,
		State:         string(d.State),
		Error:         d.Error,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
}

func documentFromModel(m *documentModel) *document.Document {
	docID, _ := id.ParseDocumentID(m.ID)
	colID, _ := id.ParseCollectionID(m.CollectionID)
	return &document.Document{
		ID:            docID,
		CollectionID:  colID,
		TenantID:      m.TenantID,
		Title:         m.Title,
		Source:        m.Source,
		SourceType:    m.SourceType,
		ContentHash:   m.ContentHash,
		ContentLength: m.ContentLength,
		ChunkCount:    m.ChunkCount,
		Metadata:      m.Metadata,
		State:         document.State(m.State),
		Error:         m.Error,
	}
}

// ──────────────────────────────────────────────────
// Chunk model
// ──────────────────────────────────────────────────

type chunkModel struct {
	bun.BaseModel `bun:"table:weave_chunks"`

	ID           string            `bun:"id,pk"`
	DocumentID   string            `bun:"document_id,notnull"`
	CollectionID string            `bun:"collection_id,notnull"`
	TenantID     string            `bun:"tenant_id,notnull"`
	Content      string            `bun:"content,notnull"`
	Index        int               `bun:"index,notnull"`
	StartOffset  int               `bun:"start_offset,notnull"`
	EndOffset    int               `bun:"end_offset,notnull"`
	TokenCount   int               `bun:"token_count,notnull"`
	Metadata     map[string]string `bun:"metadata,type:jsonb"`
	ParentID     string            `bun:"parent_id"`
	CreatedAt    time.Time         `bun:"created_at,notnull"`
}

func chunkToModel(c *chunk.Chunk) *chunkModel {
	return &chunkModel{
		ID:           c.ID.String(),
		DocumentID:   c.DocumentID.String(),
		CollectionID: c.CollectionID.String(),
		TenantID:     c.TenantID,
		Content:      c.Content,
		Index:        c.Index,
		StartOffset:  c.StartOffset,
		EndOffset:    c.EndOffset,
		TokenCount:   c.TokenCount,
		Metadata:     c.Metadata,
		ParentID:     c.ParentID,
		CreatedAt:    c.CreatedAt,
	}
}

func chunkFromModel(m *chunkModel) *chunk.Chunk {
	chkID, _ := id.ParseChunkID(m.ID)
	docID, _ := id.ParseDocumentID(m.DocumentID)
	colID, _ := id.ParseCollectionID(m.CollectionID)
	return &chunk.Chunk{
		ID:           chkID,
		DocumentID:   docID,
		CollectionID: colID,
		TenantID:     m.TenantID,
		Content:      m.Content,
		Index:        m.Index,
		StartOffset:  m.StartOffset,
		EndOffset:    m.EndOffset,
		TokenCount:   m.TokenCount,
		Metadata:     m.Metadata,
		ParentID:     m.ParentID,
		CreatedAt:    m.CreatedAt,
	}
}
