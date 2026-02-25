package extension

import "time"

// Config holds the Weave extension configuration.
// Fields can be set programmatically via ExtOption functions or loaded from
// YAML configuration files (under "extensions.weave" or "weave" keys).
type Config struct {
	// DisableRoutes prevents HTTP route registration.
	DisableRoutes bool `json:"disable_routes" mapstructure:"disable_routes" yaml:"disable_routes"`

	// DisableMigrate prevents auto-migration on start.
	DisableMigrate bool `json:"disable_migrate" mapstructure:"disable_migrate" yaml:"disable_migrate"`

	// BasePath is the URL prefix for weave routes (default: "/weave").
	BasePath string `json:"base_path" mapstructure:"base_path" yaml:"base_path"`

	// DefaultChunkSize is the default number of tokens per chunk.
	DefaultChunkSize int `json:"default_chunk_size" mapstructure:"default_chunk_size" yaml:"default_chunk_size"`

	// DefaultChunkOverlap is the default token overlap between chunks.
	DefaultChunkOverlap int `json:"default_chunk_overlap" mapstructure:"default_chunk_overlap" yaml:"default_chunk_overlap"`

	// DefaultEmbeddingModel is the default embedding model identifier.
	DefaultEmbeddingModel string `json:"default_embedding_model" mapstructure:"default_embedding_model" yaml:"default_embedding_model"`

	// DefaultChunkStrategy is the default chunking strategy (e.g., "recursive").
	DefaultChunkStrategy string `json:"default_chunk_strategy" mapstructure:"default_chunk_strategy" yaml:"default_chunk_strategy"`

	// DefaultTopK is the default number of results for similarity searches.
	DefaultTopK int `json:"default_top_k" mapstructure:"default_top_k" yaml:"default_top_k"`

	// ShutdownTimeout is the maximum time to wait for graceful shutdown.
	ShutdownTimeout time.Duration `json:"shutdown_timeout" mapstructure:"shutdown_timeout" yaml:"shutdown_timeout"`

	// IngestConcurrency controls how many ingest operations can run in parallel.
	IngestConcurrency int `json:"ingest_concurrency" mapstructure:"ingest_concurrency" yaml:"ingest_concurrency"`

	// GroveDatabase is the name of a grove.DB registered in the DI container.
	// When set, the extension resolves this named database and auto-constructs
	// the appropriate store based on the driver type (pg/sqlite/mongo).
	// When empty and WithGroveDatabase was called, the default (unnamed) DB is used.
	GroveDatabase string `json:"grove_database" mapstructure:"grove_database" yaml:"grove_database"`

	// RequireConfig requires config to be present in YAML files.
	// If true and no config is found, Register returns an error.
	RequireConfig bool `json:"-" yaml:"-"`
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
