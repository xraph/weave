// Package vectorstore defines the pluggable interface for vector storage
// and similarity search.
package vectorstore

import "context"

// VectorStore is the pluggable interface for vector storage and retrieval.
// Separate from the metadata store — this handles embeddings.
type VectorStore interface {
	// Upsert inserts or updates vector entries.
	Upsert(ctx context.Context, entries []Entry) error

	// Search returns the most similar entries to the given vector.
	Search(ctx context.Context, vector []float32, opts *SearchOptions) ([]SearchResult, error)

	// Delete removes entries by their IDs.
	Delete(ctx context.Context, ids []string) error

	// DeleteByMetadata removes entries matching the given metadata filter.
	DeleteByMetadata(ctx context.Context, filter map[string]string) error
}

// Entry represents a single vector entry in the store.
type Entry struct {
	ID       string            `json:"id"`
	Vector   []float32         `json:"vector"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata"`
}

// SearchResult is an Entry paired with a similarity score.
type SearchResult struct {
	Entry
	Score float64 `json:"score"`
}

// SearchOptions configures a vector similarity search.
type SearchOptions struct {
	// TopK is the maximum number of results to return.
	TopK int `json:"top_k"`
	// Filter restricts search to entries matching these metadata key-value pairs.
	Filter map[string]string `json:"filter"`
	// TenantKey is the namespace key for tenant isolation.
	TenantKey string `json:"tenant_key"`
	// MinScore is the minimum similarity score threshold.
	MinScore float64 `json:"min_score"`
}
