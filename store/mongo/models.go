package mongo

import (
	"time"

	"github.com/xraph/grove"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

// Collection model

type collectionModel struct {
	grove.BaseModel `grove:"table:weave_collections"`

	ID             string            `grove:"id,pk" bson:"_id"`
	Name           string            `grove:"name,notnull" bson:"name"`
	Description    string            `grove:"description" bson:"description"`
	TenantID       string            `grove:"tenant_id,notnull" bson:"tenant_id"`
	AppID          string            `grove:"app_id,notnull" bson:"app_id"`
	EmbeddingModel string            `grove:"embedding_model,notnull" bson:"embedding_model"`
	EmbeddingDims  int               `grove:"embedding_dims,notnull" bson:"embedding_dims"`
	ChunkStrategy  string            `grove:"chunk_strategy,notnull" bson:"chunk_strategy"`
	ChunkSize      int               `grove:"chunk_size,notnull" bson:"chunk_size"`
	ChunkOverlap   int               `grove:"chunk_overlap,notnull" bson:"chunk_overlap"`
	Metadata       map[string]string `grove:"metadata" bson:"metadata"`
	DocumentCount  int64             `grove:"document_count,notnull" bson:"document_count"`
	ChunkCount     int64             `grove:"chunk_count,notnull" bson:"chunk_count"`
	CreatedAt      time.Time         `grove:"created_at,notnull" bson:"created_at"`
	UpdatedAt      time.Time         `grove:"updated_at,notnull" bson:"updated_at"`
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

func collectionFromModel(m *collectionModel) (*collection.Collection, error) {
	colID, err := id.ParseCollectionID(m.ID)
	if err != nil {
		return nil, err
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
		Metadata:       m.Metadata,
		DocumentCount:  m.DocumentCount,
		ChunkCount:     m.ChunkCount,
	}, nil
}

// Document model

type documentModel struct {
	grove.BaseModel `grove:"table:weave_documents"`

	ID            string            `grove:"id,pk" bson:"_id"`
	CollectionID  string            `grove:"collection_id,notnull" bson:"collection_id"`
	TenantID      string            `grove:"tenant_id,notnull" bson:"tenant_id"`
	Title         string            `grove:"title" bson:"title"`
	Source        string            `grove:"source" bson:"source"`
	SourceType    string            `grove:"source_type" bson:"source_type"`
	ContentHash   string            `grove:"content_hash,notnull" bson:"content_hash"`
	ContentLength int               `grove:"content_length,notnull" bson:"content_length"`
	ChunkCount    int               `grove:"chunk_count,notnull" bson:"chunk_count"`
	Metadata      map[string]string `grove:"metadata" bson:"metadata"`
	State         string            `grove:"state,notnull" bson:"state"`
	Error         string            `grove:"error" bson:"error"`
	CreatedAt     time.Time         `grove:"created_at,notnull" bson:"created_at"`
	UpdatedAt     time.Time         `grove:"updated_at,notnull" bson:"updated_at"`
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

func documentFromModel(m *documentModel) (*document.Document, error) {
	docID, err := id.ParseDocumentID(m.ID)
	if err != nil {
		return nil, err
	}
	colID, err := id.ParseCollectionID(m.CollectionID)
	if err != nil {
		return nil, err
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
		Metadata:      m.Metadata,
		State:         document.State(m.State),
		Error:         m.Error,
	}, nil
}

// Chunk model

type chunkModel struct {
	grove.BaseModel `grove:"table:weave_chunks"`

	ID           string            `grove:"id,pk" bson:"_id"`
	DocumentID   string            `grove:"document_id,notnull" bson:"document_id"`
	CollectionID string            `grove:"collection_id,notnull" bson:"collection_id"`
	TenantID     string            `grove:"tenant_id,notnull" bson:"tenant_id"`
	Content      string            `grove:"content,notnull" bson:"content"`
	Index        int               `grove:"index,notnull" bson:"index"`
	StartOffset  int               `grove:"start_offset,notnull" bson:"start_offset"`
	EndOffset    int               `grove:"end_offset,notnull" bson:"end_offset"`
	TokenCount   int               `grove:"token_count,notnull" bson:"token_count"`
	Metadata     map[string]string `grove:"metadata" bson:"metadata"`
	ParentID     string            `grove:"parent_id" bson:"parent_id"`
	CreatedAt    time.Time         `grove:"created_at,notnull" bson:"created_at"`
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
	}, nil
}
