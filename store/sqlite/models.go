package sqlite

import (
	"encoding/json"
	"time"

	"github.com/xraph/grove"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

// ──────────────────────────────────────────────────
// Collection model
// ──────────────────────────────────────────────────

type collectionModel struct {
	grove.BaseModel `grove:"table:weave_collections"`

	ID             string    `grove:"id,pk"`
	Name           string    `grove:"name,notnull"`
	Description    string    `grove:"description"`
	TenantID       string    `grove:"tenant_id,notnull"`
	AppID          string    `grove:"app_id,notnull"`
	EmbeddingModel string    `grove:"embedding_model,notnull"`
	EmbeddingDims  int       `grove:"embedding_dims,notnull"`
	ChunkStrategy  string    `grove:"chunk_strategy,notnull"`
	ChunkSize      int       `grove:"chunk_size,notnull"`
	ChunkOverlap   int       `grove:"chunk_overlap,notnull"`
	Metadata       string    `grove:"metadata"`
	DocumentCount  int64     `grove:"document_count,notnull"`
	ChunkCount     int64     `grove:"chunk_count,notnull"`
	CreatedAt      time.Time `grove:"created_at,notnull"`
	UpdatedAt      time.Time `grove:"updated_at,notnull"`
}

func collectionToModel(c *collection.Collection) *collectionModel {
	metadata, _ := json.Marshal(c.Metadata) //nolint:errcheck // best-effort
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
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
		Metadata:       string(metadata),
		DocumentCount:  c.DocumentCount,
		ChunkCount:     c.ChunkCount,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}

func collectionFromModel(m *collectionModel) (*collection.Collection, error) {
	colID, err := id.ParseCollectionID(m.ID)
	if err != nil {
		return nil, err
	}
	var metadata map[string]string
	if m.Metadata != "" {
		_ = json.Unmarshal([]byte(m.Metadata), &metadata) //nolint:errcheck // best-effort
	}
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
		Metadata:       metadata,
		DocumentCount:  m.DocumentCount,
		ChunkCount:     m.ChunkCount,
	}, nil
}

// ──────────────────────────────────────────────────
// Document model
// ──────────────────────────────────────────────────

type documentModel struct {
	grove.BaseModel `grove:"table:weave_documents"`

	ID            string    `grove:"id,pk"`
	CollectionID  string    `grove:"collection_id,notnull"`
	TenantID      string    `grove:"tenant_id,notnull"`
	Title         string    `grove:"title"`
	Source        string    `grove:"source"`
	SourceType    string    `grove:"source_type"`
	ContentHash   string    `grove:"content_hash,notnull"`
	ContentLength int       `grove:"content_length,notnull"`
	ChunkCount    int       `grove:"chunk_count,notnull"`
	Metadata      string    `grove:"metadata"`
	State         string    `grove:"state,notnull"`
	Error         string    `grove:"error"`
	CreatedAt     time.Time `grove:"created_at,notnull"`
	UpdatedAt     time.Time `grove:"updated_at,notnull"`
}

func documentToModel(d *document.Document) *documentModel {
	metadata, _ := json.Marshal(d.Metadata) //nolint:errcheck // best-effort
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
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
		Metadata:      string(metadata),
		State:         string(d.State),
		Error:         d.Error,
		CreatedAt:     d.CreatedAt,
		UpdatedAt:     d.UpdatedAt,
	}
}

func documentFromModel(m *documentModel) (*document.Document, error) {
	docID, err := id.ParseDocumentID(m.ID)
	if err != nil {
		return nil, err
	}
	colID, err := id.ParseCollectionID(m.CollectionID)
	if err != nil {
		return nil, err
	}
	var metadata map[string]string
	if m.Metadata != "" {
		_ = json.Unmarshal([]byte(m.Metadata), &metadata) //nolint:errcheck // best-effort
	}
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
		Metadata:      metadata,
		State:         document.State(m.State),
		Error:         m.Error,
	}, nil
}

// ──────────────────────────────────────────────────
// Chunk model
// ──────────────────────────────────────────────────

type chunkModel struct {
	grove.BaseModel `grove:"table:weave_chunks"`

	ID           string    `grove:"id,pk"`
	DocumentID   string    `grove:"document_id,notnull"`
	CollectionID string    `grove:"collection_id,notnull"`
	TenantID     string    `grove:"tenant_id,notnull"`
	Content      string    `grove:"content,notnull"`
	Index        int       `grove:"index,notnull"`
	StartOffset  int       `grove:"start_offset,notnull"`
	EndOffset    int       `grove:"end_offset,notnull"`
	TokenCount   int       `grove:"token_count,notnull"`
	Metadata     string    `grove:"metadata"`
	ParentID     string    `grove:"parent_id"`
	CreatedAt    time.Time `grove:"created_at,notnull"`
}

func chunkToModel(c *chunk.Chunk) *chunkModel {
	metadata, _ := json.Marshal(c.Metadata) //nolint:errcheck // best-effort
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
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
		Metadata:     string(metadata),
		ParentID:     c.ParentID,
		CreatedAt:    c.CreatedAt,
	}
}

func chunkFromModel(m *chunkModel) (*chunk.Chunk, error) {
	chkID, err := id.ParseChunkID(m.ID)
	if err != nil {
		return nil, err
	}
	docID, err := id.ParseDocumentID(m.DocumentID)
	if err != nil {
		return nil, err
	}
	colID, err := id.ParseCollectionID(m.CollectionID)
	if err != nil {
		return nil, err
	}
	var metadata map[string]string
	if m.Metadata != "" {
		_ = json.Unmarshal([]byte(m.Metadata), &metadata) //nolint:errcheck // best-effort
	}
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
		Metadata:     metadata,
		ParentID:     m.ParentID,
		CreatedAt:    m.CreatedAt,
	}, nil
}
