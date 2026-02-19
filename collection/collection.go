// Package collection defines the Collection entity.
package collection

import (
	"github.com/xraph/weave"
	"github.com/xraph/weave/id"
)

// Collection represents a named group of documents with shared
// embedding and chunking configuration.
type Collection struct {
	weave.Entity

	ID             id.CollectionID   `json:"id" bun:"id,pk"`
	Name           string            `json:"name" bun:"name,notnull"`
	Description    string            `json:"description,omitempty" bun:"description"`
	TenantID       string            `json:"tenant_id" bun:"tenant_id,notnull"`
	AppID          string            `json:"app_id" bun:"app_id,notnull"`
	EmbeddingModel string            `json:"embedding_model" bun:"embedding_model,notnull,default:'text-embedding-3-small'"`
	EmbeddingDims  int               `json:"embedding_dims" bun:"embedding_dims,notnull,default:1536"`
	ChunkStrategy  string            `json:"chunk_strategy" bun:"chunk_strategy,notnull,default:'recursive'"`
	ChunkSize      int               `json:"chunk_size" bun:"chunk_size,notnull,default:512"`
	ChunkOverlap   int               `json:"chunk_overlap" bun:"chunk_overlap,notnull,default:50"`
	Metadata       map[string]string `json:"metadata" bun:"metadata,notnull,default:'{}'"`
	DocumentCount  int64             `json:"document_count" bun:"document_count,notnull,default:0"`
	ChunkCount     int64             `json:"chunk_count" bun:"chunk_count,notnull,default:0"`
}
