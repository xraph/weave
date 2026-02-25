// Package plugins defines the plugin system for Weave.
// Plugins are notified of lifecycle events (collection created, ingest
// completed, retrieval started, etc.) and can react to them — logging,
// metrics, tracing, auditing, etc.
//
// Each lifecycle hook is a separate interface so plugins opt in only
// to the events they care about.
package plugins

import (
	"context"
	"time"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

// ──────────────────────────────────────────────────
// Base extension interface
// ──────────────────────────────────────────────────

// Extension is the base interface all Weave plugins must implement.
type Extension interface {
	// Name returns a unique human-readable name for the extension.
	Name() string
}

// ──────────────────────────────────────────────────
// Collection lifecycle hooks
// ──────────────────────────────────────────────────

// CollectionCreated is called when a collection is created.
type CollectionCreated interface {
	OnCollectionCreated(ctx context.Context, col *collection.Collection) error
}

// CollectionDeleted is called when a collection is deleted.
type CollectionDeleted interface {
	OnCollectionDeleted(ctx context.Context, colID id.CollectionID) error
}

// ──────────────────────────────────────────────────
// Ingestion lifecycle hooks
// ──────────────────────────────────────────────────

// IngestStarted is called when document ingestion begins.
type IngestStarted interface {
	OnIngestStarted(ctx context.Context, colID id.CollectionID, docs []*document.Document) error
}

// IngestChunked is called after documents are chunked.
type IngestChunked interface {
	OnIngestChunked(ctx context.Context, chunks []*chunk.Chunk) error
}

// IngestEmbedded is called after chunks are embedded.
type IngestEmbedded interface {
	OnIngestEmbedded(ctx context.Context, chunks []*chunk.Chunk) error
}

// IngestCompleted is called when ingestion finishes successfully.
type IngestCompleted interface {
	OnIngestCompleted(ctx context.Context, colID id.CollectionID, docCount, chunkCount int, elapsed time.Duration) error
}

// IngestFailed is called when ingestion fails.
type IngestFailed interface {
	OnIngestFailed(ctx context.Context, colID id.CollectionID, err error) error
}

// ──────────────────────────────────────────────────
// Retrieval lifecycle hooks
// ──────────────────────────────────────────────────

// RetrievalStarted is called when a retrieval query begins.
type RetrievalStarted interface {
	OnRetrievalStarted(ctx context.Context, colID id.CollectionID, query string) error
}

// RetrievalCompleted is called when retrieval finishes successfully.
type RetrievalCompleted interface {
	OnRetrievalCompleted(ctx context.Context, colID id.CollectionID, resultCount int, elapsed time.Duration) error
}

// RetrievalFailed is called when retrieval fails.
type RetrievalFailed interface {
	OnRetrievalFailed(ctx context.Context, colID id.CollectionID, err error) error
}

// ──────────────────────────────────────────────────
// Document lifecycle hooks
// ──────────────────────────────────────────────────

// DocumentDeleted is called when a document is deleted.
type DocumentDeleted interface {
	OnDocumentDeleted(ctx context.Context, docID id.DocumentID) error
}

// ──────────────────────────────────────────────────
// Reindex lifecycle hooks
// ──────────────────────────────────────────────────

// ReindexStarted is called when a collection reindex begins.
type ReindexStarted interface {
	OnReindexStarted(ctx context.Context, colID id.CollectionID) error
}

// ReindexCompleted is called when a collection reindex finishes.
type ReindexCompleted interface {
	OnReindexCompleted(ctx context.Context, colID id.CollectionID, elapsed time.Duration) error
}

// ──────────────────────────────────────────────────
// Shutdown hook
// ──────────────────────────────────────────────────

// Shutdown is called during graceful shutdown.
type Shutdown interface {
	OnShutdown(ctx context.Context) error
}
