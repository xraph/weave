package weave

import "time"

// Config holds configuration for the Weave engine.
type Config struct {
	// DefaultChunkSize is the target chunk size in tokens.
	DefaultChunkSize int

	// DefaultChunkOverlap is the overlap between chunks in tokens.
	DefaultChunkOverlap int

	// DefaultEmbeddingModel is the embedding model name used when
	// a collection does not specify one.
	DefaultEmbeddingModel string

	// DefaultChunkStrategy is the chunking strategy used when
	// a collection does not specify one.
	DefaultChunkStrategy string

	// DefaultTopK is the default number of results for retrieval.
	DefaultTopK int

	// ShutdownTimeout is the maximum time to wait for graceful shutdown.
	ShutdownTimeout time.Duration

	// IngestConcurrency is the maximum number of documents processed
	// concurrently during batch ingestion.
	IngestConcurrency int
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		DefaultChunkSize:      512,
		DefaultChunkOverlap:   50,
		DefaultEmbeddingModel: "text-embedding-3-small",
		DefaultChunkStrategy:  "recursive",
		DefaultTopK:           10,
		ShutdownTimeout:       30 * time.Second,
		IngestConcurrency:     4,
	}
}
