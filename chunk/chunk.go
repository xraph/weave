// Package chunk defines the Chunk entity.
package chunk

import (
	"time"

	"github.com/xraph/weave/id"
)

// Chunk represents a portion of a document that has been split for
// embedding and retrieval.
type Chunk struct {
	ID           id.ChunkID        `json:"id" bun:"id,pk"`
	DocumentID   id.DocumentID     `json:"document_id" bun:"document_id,notnull"`
	CollectionID id.CollectionID   `json:"collection_id" bun:"collection_id,notnull"`
	TenantID     string            `json:"tenant_id" bun:"tenant_id,notnull"`
	Content      string            `json:"content" bun:"content,notnull"`
	Index        int               `json:"index" bun:"index,notnull"`
	StartOffset  int               `json:"start_offset" bun:"start_offset,notnull,default:0"`
	EndOffset    int               `json:"end_offset" bun:"end_offset,notnull,default:0"`
	TokenCount   int               `json:"token_count" bun:"token_count,notnull,default:0"`
	Metadata     map[string]string `json:"metadata" bun:"metadata,notnull,default:'{}'"`
	ParentID     string            `json:"parent_id,omitempty" bun:"parent_id"`
	CreatedAt    time.Time         `json:"created_at" bun:"created_at,notnull,default:current_timestamp"`
}
