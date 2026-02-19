// Package chunker defines the interface for splitting text into manageable
// chunks for embedding and retrieval.
package chunker

import "context"

// ChunkResult represents a single chunk produced by a chunker.
type ChunkResult struct {
	// Content is the text content of the chunk.
	Content string `json:"content"`
	// Index is the zero-based position of this chunk in the document.
	Index int `json:"index"`
	// StartOffset is the byte offset of the chunk start in the original text.
	StartOffset int `json:"start_offset"`
	// EndOffset is the byte offset of the chunk end in the original text.
	EndOffset int `json:"end_offset"`
	// TokenCount is the estimated number of tokens in this chunk.
	TokenCount int `json:"token_count"`
	// Metadata holds chunker-specific metadata.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Options configures the chunking behaviour.
type Options struct {
	// ChunkSize is the target chunk size in tokens.
	ChunkSize int
	// ChunkOverlap is the number of overlapping tokens between chunks.
	ChunkOverlap int
	// Strategy is the chunking strategy name (e.g. "recursive", "fixed").
	Strategy string
}

// Chunker splits text into chunks for embedding.
type Chunker interface {
	// Chunk splits the given text into chunks according to the options.
	Chunk(ctx context.Context, text string, opts *Options) ([]ChunkResult, error)
}
