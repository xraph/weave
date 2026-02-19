// Package retriever defines the interface for retrieving relevant chunks
// based on a query.
package retriever

import (
	"context"

	"github.com/xraph/weave/chunk"
)

// Result represents a retrieved chunk with a relevance score.
type Result struct {
	// Chunk is the retrieved chunk.
	Chunk *chunk.Chunk `json:"chunk"`
	// Score is the relevance score (higher is more relevant).
	Score float64 `json:"score"`
}

// Options configures a retrieval operation.
type Options struct {
	// CollectionID restricts retrieval to a specific collection.
	CollectionID string `json:"collection_id,omitempty"`
	// TenantKey is the namespace key for tenant isolation.
	TenantKey string `json:"tenant_key,omitempty"`
	// TopK is the maximum number of results to return.
	TopK int `json:"top_k"`
	// MinScore is the minimum relevance score threshold.
	MinScore float64 `json:"min_score"`
	// Filter restricts retrieval to chunks matching these metadata key-value pairs.
	Filter map[string]string `json:"filter,omitempty"`
}

// Retriever retrieves relevant chunks for a given query.
type Retriever interface {
	// Retrieve returns the most relevant chunks for the given query.
	Retrieve(ctx context.Context, query string, opts *Options) ([]Result, error)
}
