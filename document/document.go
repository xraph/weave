// Package document defines the Document entity and its lifecycle states.
package document

import (
	"github.com/xraph/weave"
	"github.com/xraph/weave/id"
)

// State represents the lifecycle state of a document.
type State string

const (
	// StatePending means the document has been created but not yet processed.
	StatePending State = "pending"
	// StateProcessing means the document is currently being chunked/embedded.
	StateProcessing State = "processing"
	// StateReady means the document has been fully processed and is searchable.
	StateReady State = "ready"
	// StateFailed means the document processing failed.
	StateFailed State = "failed"
)

// Document represents an ingested document within a collection.
type Document struct {
	weave.Entity

	ID            id.DocumentID   `json:"id" bun:"id,pk"`
	CollectionID  id.CollectionID `json:"collection_id" bun:"collection_id,notnull"`
	TenantID      string          `json:"tenant_id" bun:"tenant_id,notnull"`
	Title         string          `json:"title,omitempty" bun:"title"`
	Source        string          `json:"source,omitempty" bun:"source"`
	SourceType    string          `json:"source_type,omitempty" bun:"source_type"`
	ContentHash   string          `json:"content_hash" bun:"content_hash,notnull"`
	ContentLength int             `json:"content_length" bun:"content_length,notnull,default:0"`
	ChunkCount    int             `json:"chunk_count" bun:"chunk_count,notnull,default:0"`
	Metadata      map[string]string `json:"metadata" bun:"metadata,notnull,default:'{}'"`
	State         State           `json:"state" bun:"state,notnull,default:'pending'"`
	Error         string          `json:"error,omitempty" bun:"error"`
}
