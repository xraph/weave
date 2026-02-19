package ext

import (
	"context"
	"log/slog"
	"time"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

// Named entry types pair a hook implementation with the extension name
// captured at registration time.
type collectionCreatedEntry struct {
	name string
	hook CollectionCreated
}

type collectionDeletedEntry struct {
	name string
	hook CollectionDeleted
}

type ingestStartedEntry struct {
	name string
	hook IngestStarted
}

type ingestChunkedEntry struct {
	name string
	hook IngestChunked
}

type ingestEmbeddedEntry struct {
	name string
	hook IngestEmbedded
}

type ingestCompletedEntry struct {
	name string
	hook IngestCompleted
}

type ingestFailedEntry struct {
	name string
	hook IngestFailed
}

type retrievalStartedEntry struct {
	name string
	hook RetrievalStarted
}

type retrievalCompletedEntry struct {
	name string
	hook RetrievalCompleted
}

type retrievalFailedEntry struct {
	name string
	hook RetrievalFailed
}

type documentDeletedEntry struct {
	name string
	hook DocumentDeleted
}

type reindexStartedEntry struct {
	name string
	hook ReindexStarted
}

type reindexCompletedEntry struct {
	name string
	hook ReindexCompleted
}

type shutdownEntry struct {
	name string
	hook Shutdown
}

// Registry holds registered extensions and dispatches lifecycle events
// to them. It type-caches extensions at registration time so emit calls
// iterate only over extensions that implement the relevant hook.
type Registry struct {
	extensions []Extension
	logger     *slog.Logger

	// Type-cached slices for each lifecycle hook.
	collectionCreated  []collectionCreatedEntry
	collectionDeleted  []collectionDeletedEntry
	ingestStarted      []ingestStartedEntry
	ingestChunked      []ingestChunkedEntry
	ingestEmbedded     []ingestEmbeddedEntry
	ingestCompleted    []ingestCompletedEntry
	ingestFailed       []ingestFailedEntry
	retrievalStarted   []retrievalStartedEntry
	retrievalCompleted []retrievalCompletedEntry
	retrievalFailed    []retrievalFailedEntry
	documentDeleted    []documentDeletedEntry
	reindexStarted     []reindexStartedEntry
	reindexCompleted   []reindexCompletedEntry
	shutdown           []shutdownEntry
}

// NewRegistry creates an extension registry with the given logger.
func NewRegistry(logger *slog.Logger) *Registry {
	return &Registry{logger: logger}
}

// Register adds an extension and type-asserts it into all applicable
// hook caches. Extensions are notified in registration order.
func (r *Registry) Register(e Extension) {
	r.extensions = append(r.extensions, e)
	name := e.Name()

	if h, ok := e.(CollectionCreated); ok {
		r.collectionCreated = append(r.collectionCreated, collectionCreatedEntry{name, h})
	}
	if h, ok := e.(CollectionDeleted); ok {
		r.collectionDeleted = append(r.collectionDeleted, collectionDeletedEntry{name, h})
	}
	if h, ok := e.(IngestStarted); ok {
		r.ingestStarted = append(r.ingestStarted, ingestStartedEntry{name, h})
	}
	if h, ok := e.(IngestChunked); ok {
		r.ingestChunked = append(r.ingestChunked, ingestChunkedEntry{name, h})
	}
	if h, ok := e.(IngestEmbedded); ok {
		r.ingestEmbedded = append(r.ingestEmbedded, ingestEmbeddedEntry{name, h})
	}
	if h, ok := e.(IngestCompleted); ok {
		r.ingestCompleted = append(r.ingestCompleted, ingestCompletedEntry{name, h})
	}
	if h, ok := e.(IngestFailed); ok {
		r.ingestFailed = append(r.ingestFailed, ingestFailedEntry{name, h})
	}
	if h, ok := e.(RetrievalStarted); ok {
		r.retrievalStarted = append(r.retrievalStarted, retrievalStartedEntry{name, h})
	}
	if h, ok := e.(RetrievalCompleted); ok {
		r.retrievalCompleted = append(r.retrievalCompleted, retrievalCompletedEntry{name, h})
	}
	if h, ok := e.(RetrievalFailed); ok {
		r.retrievalFailed = append(r.retrievalFailed, retrievalFailedEntry{name, h})
	}
	if h, ok := e.(DocumentDeleted); ok {
		r.documentDeleted = append(r.documentDeleted, documentDeletedEntry{name, h})
	}
	if h, ok := e.(ReindexStarted); ok {
		r.reindexStarted = append(r.reindexStarted, reindexStartedEntry{name, h})
	}
	if h, ok := e.(ReindexCompleted); ok {
		r.reindexCompleted = append(r.reindexCompleted, reindexCompletedEntry{name, h})
	}
	if h, ok := e.(Shutdown); ok {
		r.shutdown = append(r.shutdown, shutdownEntry{name, h})
	}
}

// Extensions returns all registered extensions.
func (r *Registry) Extensions() []Extension { return r.extensions }

// ──────────────────────────────────────────────────
// Collection event emitters
// ──────────────────────────────────────────────────

// EmitCollectionCreated notifies all extensions that implement CollectionCreated.
func (r *Registry) EmitCollectionCreated(ctx context.Context, col *collection.Collection) {
	for _, e := range r.collectionCreated {
		if err := e.hook.OnCollectionCreated(ctx, col); err != nil {
			r.logHookError("OnCollectionCreated", e.name, err)
		}
	}
}

// EmitCollectionDeleted notifies all extensions that implement CollectionDeleted.
func (r *Registry) EmitCollectionDeleted(ctx context.Context, colID id.CollectionID) {
	for _, e := range r.collectionDeleted {
		if err := e.hook.OnCollectionDeleted(ctx, colID); err != nil {
			r.logHookError("OnCollectionDeleted", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Ingestion event emitters
// ──────────────────────────────────────────────────

// EmitIngestStarted notifies all extensions that implement IngestStarted.
func (r *Registry) EmitIngestStarted(ctx context.Context, colID id.CollectionID, docs []*document.Document) {
	for _, e := range r.ingestStarted {
		if err := e.hook.OnIngestStarted(ctx, colID, docs); err != nil {
			r.logHookError("OnIngestStarted", e.name, err)
		}
	}
}

// EmitIngestChunked notifies all extensions that implement IngestChunked.
func (r *Registry) EmitIngestChunked(ctx context.Context, chunks []*chunk.Chunk) {
	for _, e := range r.ingestChunked {
		if err := e.hook.OnIngestChunked(ctx, chunks); err != nil {
			r.logHookError("OnIngestChunked", e.name, err)
		}
	}
}

// EmitIngestEmbedded notifies all extensions that implement IngestEmbedded.
func (r *Registry) EmitIngestEmbedded(ctx context.Context, chunks []*chunk.Chunk) {
	for _, e := range r.ingestEmbedded {
		if err := e.hook.OnIngestEmbedded(ctx, chunks); err != nil {
			r.logHookError("OnIngestEmbedded", e.name, err)
		}
	}
}

// EmitIngestCompleted notifies all extensions that implement IngestCompleted.
func (r *Registry) EmitIngestCompleted(ctx context.Context, colID id.CollectionID, docCount, chunkCount int, elapsed time.Duration) {
	for _, e := range r.ingestCompleted {
		if err := e.hook.OnIngestCompleted(ctx, colID, docCount, chunkCount, elapsed); err != nil {
			r.logHookError("OnIngestCompleted", e.name, err)
		}
	}
}

// EmitIngestFailed notifies all extensions that implement IngestFailed.
func (r *Registry) EmitIngestFailed(ctx context.Context, colID id.CollectionID, ingestErr error) {
	for _, e := range r.ingestFailed {
		if err := e.hook.OnIngestFailed(ctx, colID, ingestErr); err != nil {
			r.logHookError("OnIngestFailed", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Retrieval event emitters
// ──────────────────────────────────────────────────

// EmitRetrievalStarted notifies all extensions that implement RetrievalStarted.
func (r *Registry) EmitRetrievalStarted(ctx context.Context, colID id.CollectionID, query string) {
	for _, e := range r.retrievalStarted {
		if err := e.hook.OnRetrievalStarted(ctx, colID, query); err != nil {
			r.logHookError("OnRetrievalStarted", e.name, err)
		}
	}
}

// EmitRetrievalCompleted notifies all extensions that implement RetrievalCompleted.
func (r *Registry) EmitRetrievalCompleted(ctx context.Context, colID id.CollectionID, resultCount int, elapsed time.Duration) {
	for _, e := range r.retrievalCompleted {
		if err := e.hook.OnRetrievalCompleted(ctx, colID, resultCount, elapsed); err != nil {
			r.logHookError("OnRetrievalCompleted", e.name, err)
		}
	}
}

// EmitRetrievalFailed notifies all extensions that implement RetrievalFailed.
func (r *Registry) EmitRetrievalFailed(ctx context.Context, colID id.CollectionID, retrievalErr error) {
	for _, e := range r.retrievalFailed {
		if err := e.hook.OnRetrievalFailed(ctx, colID, retrievalErr); err != nil {
			r.logHookError("OnRetrievalFailed", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Document event emitters
// ──────────────────────────────────────────────────

// EmitDocumentDeleted notifies all extensions that implement DocumentDeleted.
func (r *Registry) EmitDocumentDeleted(ctx context.Context, docID id.DocumentID) {
	for _, e := range r.documentDeleted {
		if err := e.hook.OnDocumentDeleted(ctx, docID); err != nil {
			r.logHookError("OnDocumentDeleted", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Reindex event emitters
// ──────────────────────────────────────────────────

// EmitReindexStarted notifies all extensions that implement ReindexStarted.
func (r *Registry) EmitReindexStarted(ctx context.Context, colID id.CollectionID) {
	for _, e := range r.reindexStarted {
		if err := e.hook.OnReindexStarted(ctx, colID); err != nil {
			r.logHookError("OnReindexStarted", e.name, err)
		}
	}
}

// EmitReindexCompleted notifies all extensions that implement ReindexCompleted.
func (r *Registry) EmitReindexCompleted(ctx context.Context, colID id.CollectionID, elapsed time.Duration) {
	for _, e := range r.reindexCompleted {
		if err := e.hook.OnReindexCompleted(ctx, colID, elapsed); err != nil {
			r.logHookError("OnReindexCompleted", e.name, err)
		}
	}
}

// ──────────────────────────────────────────────────
// Shutdown event emitter
// ──────────────────────────────────────────────────

// EmitShutdown notifies all extensions that implement Shutdown.
func (r *Registry) EmitShutdown(ctx context.Context) {
	for _, e := range r.shutdown {
		if err := e.hook.OnShutdown(ctx); err != nil {
			r.logHookError("OnShutdown", e.name, err)
		}
	}
}

// logHookError logs a warning when a lifecycle hook returns an error.
// Errors from hooks are never propagated — they must not block the pipeline.
func (r *Registry) logHookError(hook, extName string, err error) {
	r.logger.Warn("extension hook error",
		slog.String("hook", hook),
		slog.String("extension", extName),
		slog.String("error", err.Error()),
	)
}
