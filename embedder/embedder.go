// Package embedder defines the interface for generating vector embeddings
// from text.
package embedder

import "context"

// EmbedResult contains the embedding vector for a single text input.
type EmbedResult struct {
	// Vector is the embedding vector.
	Vector []float32 `json:"vector"`
	// TokenCount is the number of tokens consumed.
	TokenCount int `json:"token_count"`
}

// Embedder generates vector embeddings from text.
type Embedder interface {
	// Embed generates embeddings for the given texts.
	Embed(ctx context.Context, texts []string) ([]EmbedResult, error)

	// Dimensions returns the dimensionality of the embeddings produced
	// by this embedder.
	Dimensions() int
}
