package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	log "github.com/xraph/go-utils/log"
	gometrics "github.com/xraph/go-utils/metrics"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/telemetry"
	"github.com/xraph/weave/loader"
	"github.com/xraph/weave/plugins"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/vectorstore"
)

// Engine is the central coordinator for the Weave RAG pipeline.
type Engine struct {
	config      weave.Config
	logger      log.Logger
	store       store.Store
	vectorStore vectorstore.VectorStore
	embedder    embedder.Embedder
	chunker     chunker.Chunker
	loader      loader.Loader
	retriever   retriever.Retriever
	extensions  *plugins.Registry
	pendingExts []plugins.Extension

	metricsSink gometrics.Metrics
	metrics     engineMetrics
	kinds       kinds

	recovery     RecoveryConfig
	recoveryMu   sync.Mutex
	recoveryStop context.CancelFunc // nil while no recovery loop runs
	recoveryDone chan struct{}      // closed when the loop returns
}

// New creates a new Engine with the given options.
func New(opts ...Option) (*Engine, error) {
	e := &Engine{
		config:   weave.DefaultConfig(),
		logger:   log.NewNoopLogger(),
		recovery: RecoveryConfig{StalledAfter: DefaultStalledAfter},
	}
	for _, opt := range opts {
		if err := opt(e); err != nil {
			return nil, fmt.Errorf("weave: apply option: %w", err)
		}
	}

	// Components are fixed from here on, so their telemetry labels are too.
	e.kinds = e.describeKinds()
	e.metrics = newEngineMetrics(e.metricsSink)

	// Wire up the extension registry.
	e.extensions = plugins.NewRegistry(e.logger)
	for _, extension := range e.pendingExts {
		e.extensions.Register(extension)
	}
	e.pendingExts = nil

	return e, nil
}

// Health checks the health of the engine by pinging its store.
func (e *Engine) Health(ctx context.Context) error {
	if e.store != nil {
		return e.store.Ping(ctx)
	}
	return nil
}

// Start starts the engine's background work: with a RecoveryConfig
// Interval above zero and a store, the recovery loop, which runs
// RecoverStalled every Interval until Stop. Calling it again while the loop
// runs does nothing.
func (e *Engine) Start(ctx context.Context) error {
	e.startRecovery(ctx)
	return nil
}

// Stop gracefully shuts down the engine. It stops the recovery loop and
// waits for a sweep in progress to return before it closes the store.
func (e *Engine) Stop(ctx context.Context) error {
	e.stopRecovery()
	if e.extensions != nil {
		e.extensions.EmitShutdown(ctx)
	}
	if e.store != nil {
		return e.store.Close()
	}
	return nil
}

// Store returns the engine's metadata store.
func (e *Engine) Store() store.Store { return e.store }

// Logger returns the engine's logger.
func (e *Engine) Logger() log.Logger { return e.logger }

// Config returns a copy of the engine's configuration.
func (e *Engine) Config() weave.Config { return e.config }

// Extensions returns the extension registry.
func (e *Engine) Extensions() *plugins.Registry { return e.extensions }

// ──────────────────────────────────────────────────
// Collection operations
// ──────────────────────────────────────────────────

// CreateCollection creates a new collection.
func (e *Engine) CreateCollection(ctx context.Context, col *collection.Collection) error {
	if e.store == nil {
		return weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return err
	}
	switch col.TenantID {
	case "":
		col.TenantID = tenant
	case tenant:
	default:
		return fmt.Errorf("%w: tenant on the collection does not match the context tenant", weave.ErrInvalidArgument)
	}

	if col.ID.String() == "" {
		col.ID = id.NewCollectionID()
	}
	if col.AppID == "" {
		col.AppID = weave.AppFromContext(ctx)
	}
	if col.EmbeddingModel == "" {
		col.EmbeddingModel = e.config.DefaultEmbeddingModel
	}
	if col.ChunkStrategy == "" {
		col.ChunkStrategy = e.config.DefaultChunkStrategy
	}
	if col.ChunkSize == 0 {
		col.ChunkSize = e.config.DefaultChunkSize
	}
	if col.ChunkOverlap == 0 {
		col.ChunkOverlap = e.config.DefaultChunkOverlap
	}

	if err := e.store.CreateCollection(ctx, col); err != nil {
		return err
	}

	e.extensions.EmitCollectionCreated(ctx, col)
	return nil
}

// GetCollection retrieves a collection by ID within the context tenant.
func (e *Engine) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	return e.getCollection(ctx, tenant, colID)
}

// getCollection reads a collection and checks it belongs to tenant.
func (e *Engine) getCollection(ctx context.Context, tenant string, colID id.CollectionID) (*collection.Collection, error) {
	col, err := e.store.GetCollection(ctx, colID)
	if err != nil {
		return nil, err
	}
	return ownCollection(col, tenant)
}

// GetCollectionByName retrieves a collection by name within the context
// tenant.
func (e *Engine) GetCollectionByName(ctx context.Context, name string) (*collection.Collection, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	col, err := e.store.GetCollectionByName(ctx, tenant, name)
	if err != nil {
		return nil, err
	}
	return ownCollection(col, tenant)
}

// ListCollections returns the context tenant's collections matching the
// given filter.
func (e *Engine) ListCollections(ctx context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	cols, err := e.store.ListCollections(ctx, filter)
	if err != nil {
		return nil, err
	}
	return owned(cols, tenant, collectionTenant), nil
}

// DeleteCollection removes a collection and all its documents, chunks and
// vectors. Another tenant's collection is ErrCollectionNotFound, and nothing
// is deleted.
func (e *Engine) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	if e.store == nil {
		return weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return err
	}
	if _, err = e.getCollection(ctx, tenant, colID); err != nil {
		return err
	}

	// Delete chunks and documents first.
	if err := e.store.DeleteChunksByCollection(ctx, colID); err != nil {
		return fmt.Errorf("weave: delete chunks for collection: %w", err)
	}
	if err := e.store.DeleteDocumentsByCollection(ctx, colID); err != nil {
		return fmt.Errorf("weave: delete documents for collection: %w", err)
	}

	// Delete vector entries for the collection.
	if e.vectorStore != nil {
		if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
			"collection_id": colID.String(),
			"tenant_id":     tenant,
		}); err != nil {
			// The error's text can quote a DSN, a host or a tenant key, so
			// only its type is logged.
			e.logger.Warn("failed to delete vector entries for collection",
				log.String("collection_id", colID.String()),
				log.String("error_type", telemetry.ErrorType(err)),
			)
		}
	}

	if err := e.store.DeleteCollection(ctx, colID); err != nil {
		return err
	}

	e.extensions.EmitCollectionDeleted(ctx, colID)
	return nil
}

// CollectionStatsResult holds aggregate statistics for a collection.
type CollectionStatsResult struct {
	CollectionID   id.CollectionID `json:"collection_id"`
	CollectionName string          `json:"collection_name"`
	DocumentCount  int64           `json:"document_count"`
	ChunkCount     int64           `json:"chunk_count"`
	EmbeddingModel string          `json:"embedding_model"`
	ChunkStrategy  string          `json:"chunk_strategy"`
}

// CollectionStats returns aggregate statistics for a collection.
func (e *Engine) CollectionStats(ctx context.Context, colID id.CollectionID) (*CollectionStatsResult, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}

	col, err := e.getCollection(ctx, tenant, colID)
	if err != nil {
		return nil, err
	}

	docCount, err := e.store.CountDocuments(ctx, &document.CountFilter{CollectionID: colID})
	if err != nil {
		return nil, fmt.Errorf("weave: count documents: %w", err)
	}

	chunkCount, err := e.store.CountChunks(ctx, &chunk.CountFilter{CollectionID: colID})
	if err != nil {
		return nil, fmt.Errorf("weave: count chunks: %w", err)
	}

	return &CollectionStatsResult{
		CollectionID:   colID,
		CollectionName: col.Name,
		DocumentCount:  docCount,
		ChunkCount:     chunkCount,
		EmbeddingModel: col.EmbeddingModel,
		ChunkStrategy:  col.ChunkStrategy,
	}, nil
}

// ──────────────────────────────────────────────────
// Ingestion
// ──────────────────────────────────────────────────

// IngestInput describes a document ingestion request.
type IngestInput struct {
	// CollectionID is the target collection.
	CollectionID id.CollectionID `json:"collection_id"`
	// Title is an optional document title.
	Title string `json:"title,omitempty"`
	// Source is the document source identifier (URL, path, etc.).
	Source string `json:"source,omitempty"`
	// SourceType is the MIME type or format hint.
	SourceType string `json:"source_type,omitempty"`
	// Content is the raw document content.
	Content string `json:"content"`
	// Metadata is optional document metadata.
	Metadata map[string]string `json:"metadata,omitempty"`
	// SourceKey makes the ingest an upsert: the new content replaces this
	// source's current document in the collection. Up to 1024 bytes, not blank.
	SourceKey string `json:"source_key,omitempty"`
}

// IngestResult contains the outcome of a document ingestion.
type IngestResult struct {
	DocumentID id.DocumentID   `json:"document_id"`
	ChunkCount int             `json:"chunk_count"`
	State      document.State  `json:"state"`
	Unchanged  bool            `json:"unchanged,omitempty"` // same source, same content: nothing was embedded
	Replaced   []id.DocumentID `json:"replaced,omitempty"`  // previous versions this ingest superseded
}

// Ingest processes a single document: load, chunk, embed, and store. With a
// SourceKey it is an upsert of that source (see IngestInput.SourceKey): the
// same content again embeds nothing and answers Unchanged, and new content
// replaces the source's current document, whose ID is then in Replaced.
// Content another ingest is still working on is answered with that document
// in its current state, pending or processing, and nothing embedded.
//
// The ingest sends a heartbeat as it goes (see RecoveryConfig). When its
// document stops being pending or processing under it, because recovery
// failed it or it was deleted, Ingest deletes what it wrote and returns
// weave.ErrIngestInterrupted.
func (e *Engine) Ingest(ctx context.Context, input *IngestInput) (*IngestResult, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	if e.embedder == nil {
		return nil, weave.ErrNoEmbedder
	}
	if e.vectorStore == nil {
		return nil, weave.ErrNoVectorStore
	}
	if e.chunker == nil {
		return nil, weave.ErrNoChunker
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	if input.Content == "" {
		return nil, weave.ErrEmptyContent
	}
	if input.SourceKey != "" {
		if keyErr := checkSourceKey(input.SourceKey); keyErr != nil {
			return nil, keyErr
		}
	}

	ctx, span := telemetry.Tracer().Start(ctx, "ingest document", trace.WithAttributes(
		telemetry.CollectionID.String(input.CollectionID.String()),
		telemetry.ChunkerKind.String(e.kinds.chunker),
		telemetry.VectorStoreKind.String(e.kinds.vectorStore),
		telemetry.ContentBytes.Int(len(input.Content)),
	))
	defer span.End()

	start := time.Now()
	var res *IngestResult
	if input.SourceKey != "" {
		res, err = e.ingestSource(ctx, span, tenant, input)
		if res != nil {
			span.SetAttributes(
				telemetry.IngestUnchanged.Bool(res.Unchanged),
				telemetry.IngestReplaced.Int(len(res.Replaced)),
			)
		}
	} else {
		res, err = e.ingest(ctx, span, tenant, input)
	}

	result := telemetry.ResultOK
	switch {
	case errors.Is(err, weave.ErrCollectionNotFound):
		// The caller named a collection that isn't there: a 404, not a
		// failure of the pipeline.
		result = telemetry.ResultNotFound
	case err != nil:
		result = telemetry.ResultError
		telemetry.Fail(span, err)
	}
	span.SetAttributes(telemetry.Result.String(result))
	if res != nil {
		span.SetAttributes(telemetry.DocumentID.String(res.DocumentID.String()))
	}
	e.metrics.ingest.Observe(time.Since(start).Seconds(), e.kinds.chunker, result)
	return res, err
}

// ingest is Ingest's body, run under span for tenantID. It sets
// weave.chunk.count on the span once the content is chunked, so a document
// that fails later still says how many chunks it had.
func (e *Engine) ingest(ctx context.Context, span trace.Span, tenantID string, input *IngestInput) (*IngestResult, error) {
	start := time.Now()

	// The collection must exist within the tenant before anything is
	// created or embedded.
	col, err := e.getCollection(ctx, tenantID, input.CollectionID)
	if err != nil {
		return nil, err
	}

	doc := newDocument(tenantID, input, contentHash(input.Content))
	if createErr := e.createUnkeyed(ctx, tenantID, doc); createErr != nil {
		return nil, fmt.Errorf("weave: create document: %w", createErr)
	}

	e.extensions.EmitIngestStarted(ctx, input.CollectionID, []*document.Document{doc})

	// The first heartbeat marks the document processing.
	beat := func() error { return e.heartbeat(ctx, doc.ID) }
	if beatErr := beat(); beatErr != nil {
		return e.endIngest(ctx, doc, nil, false, beatErr)
	}

	chunks, entries, err := e.embedChunks(ctx, span, col, doc, input, beat)
	if err != nil {
		return e.endIngest(ctx, doc, nil, false, err)
	}

	// Store chunks in metadata store.
	if storeErr := e.store.CreateChunkBatch(ctx, chunks); storeErr != nil {
		return e.endIngest(ctx, doc, nil, true, fmt.Errorf("store chunks: %w", storeErr))
	}

	// Upsert vector entries.
	if upsertErr := e.upsertVectors(ctx, entries); upsertErr != nil {
		return e.endIngest(ctx, doc, entries, true, fmt.Errorf("upsert vectors: %w", upsertErr))
	}

	// The last heartbeat, right before the move to ready, keeps the document
	// clear of recovery's cutoff. The move itself is the final check: it is
	// guarded, so a document recovery failed meanwhile never turns ready,
	// and this ingest takes back the chunks and vectors it wrote.
	if beatErr := beat(); beatErr != nil {
		return e.endIngest(ctx, doc, entries, true, beatErr)
	}
	if readyErr := e.completeDocument(ctx, doc.ID, len(chunks)); readyErr != nil {
		return e.endIngest(ctx, doc, entries, true, readyErr)
	}

	elapsed := time.Since(start)
	e.extensions.EmitIngestCompleted(ctx, input.CollectionID, 1, len(chunks), elapsed)

	return &IngestResult{
		DocumentID: doc.ID,
		ChunkCount: len(chunks),
		State:      document.StateReady,
	}, nil
}

// createUnkeyed creates the pending document of an unkeyed ingest. A
// collection holds one document per content hash, so the create collides
// with any earlier document of the same content, and that one may be dead:
// failed (an ingest that failed, or one recovery gave up on), superseded,
// or pending or processing with no heartbeat for StalledAfter. A dead copy
// is removed, row, chunks and vectors, and the create is tried once more.
// weave.ErrDuplicateDocument is only the answer while a live document
// holds the content: ready, or pending or processing with a recent
// heartbeat (another ingest at work), or one that a concurrent ingest
// created between the two tries.
func (e *Engine) createUnkeyed(ctx context.Context, tenant string, doc *document.Document) error {
	err := e.store.CreateDocument(ctx, doc)
	if !errors.Is(err, weave.ErrDuplicateDocument) {
		return err
	}
	cleared, clearErr := e.clearDeadCopy(ctx, tenant, doc.CollectionID, doc.ContentHash)
	if clearErr != nil {
		return clearErr
	}
	if !cleared {
		return err
	}
	return e.store.CreateDocument(ctx, doc)
}

// clearDeadCopy removes the document of colID with hash when it is dead
// (see createUnkeyed) and reports whether the content is free now. Failed
// and superseded are final states, so nothing can bring such a document
// back between the read and the delete. A stalled pending or processing one
// is first marked failed with recovery's guarded write and cutoff, so an
// ingest that comes back to life keeps it; when that write goes through,
// IngestFailed fires once for it, as recovery would.
func (e *Engine) clearDeadCopy(ctx context.Context, tenant string, colID id.CollectionID, hash string) (bool, error) {
	docs, err := e.store.ListDocuments(ctx, &document.ListFilter{CollectionID: colID, ContentHash: hash})
	if err != nil {
		return false, fmt.Errorf("weave: find the document of this content: %w", err)
	}
	doc := sameContent(owned(docs, tenant, documentTenant), hash)
	if doc == nil {
		// Gone since the create collided: the content may be free.
		return true, nil
	}
	switch doc.State {
	case document.StateReady:
		return false, nil
	case document.StateFailed, document.StateSuperseded:
	default:
		cutoff := e.stalledBefore()
		if !doc.UpdatedAt.Before(cutoff) {
			return false, nil
		}
		failErr := e.store.FailDocument(ctx, doc.ID, weave.ErrIngestInterrupted.Error(), cutoff)
		switch {
		case errors.Is(failErr, weave.ErrInvalidState), errors.Is(failErr, weave.ErrDocumentNotFound):
			// A heartbeat, a finish or recovery got there first.
			return false, nil
		case failErr != nil:
			return false, fmt.Errorf("weave: fail a stalled document of this content: %w", failErr)
		}
		e.extensions.EmitIngestFailed(ctx, colID, weave.ErrIngestInterrupted)
	}
	if err := e.purgeDocument(ctx, tenant, doc.ID); err != nil {
		return false, fmt.Errorf("weave: purge a dead document of this content: %w", err)
	}
	return true, nil
}

// completeDocument moves an unkeyed ingest's document from processing to
// ready with chunkCount, through the store's guarded CompleteDocument. The
// write runs on a cleanup context (see cleanupContext), like failIngest's,
// so a request cancelled this late can't leave the document processing for
// recovery to fail after the caller was told it succeeded. A document that
// is no longer processing, or is gone, is weave.ErrIngestInterrupted; any
// other error is the store's, for failIngest.
func (e *Engine) completeDocument(ctx context.Context, docID id.DocumentID, chunkCount int) error {
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	err := e.store.CompleteDocument(cleanup, docID, chunkCount)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, weave.ErrInvalidState), errors.Is(err, weave.ErrDocumentNotFound):
		return weave.ErrIngestInterrupted
	}
	return fmt.Errorf("mark ready: %w", err)
}

// contentHash is the hex SHA-256 of content, the document's ContentHash.
func contentHash(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
}

// newDocument builds the pending document record for input under tenantID.
func newDocument(tenantID string, input *IngestInput, hash string) *document.Document {
	return &document.Document{
		Entity:        weave.NewEntity(),
		ID:            id.NewDocumentID(),
		CollectionID:  input.CollectionID,
		TenantID:      tenantID,
		Title:         input.Title,
		Source:        input.Source,
		SourceType:    input.SourceType,
		ContentHash:   hash,
		ContentLength: len(input.Content),
		Metadata:      input.Metadata,
		SourceKey:     input.SourceKey,
		State:         document.StatePending,
	}
}

// embedChunks loads, chunks and embeds input's content for doc, and returns
// the chunk rows and their vector entries, which carry doc's source key. It
// sets weave.chunk.count on span once the content is chunked and fires the
// chunked and embedded hooks. It calls beat after chunking and after
// embedding and stops with beat's error, weave.ErrIngestInterrupted when
// the document has moved on. Any other error is ready for failIngest.
func (e *Engine) embedChunks(ctx context.Context, span trace.Span, col *collection.Collection, doc *document.Document, input *IngestInput, beat func() error) ([]*chunk.Chunk, []vectorstore.Entry, error) {
	// Optionally load/extract text.
	content := input.Content
	if e.loader != nil && input.SourceType != "" && e.loader.Supports(input.SourceType) {
		result, loadErr := e.loader.Load(ctx, strings.NewReader(content))
		if loadErr != nil {
			return nil, nil, loadErr
		}
		content = result.Content
	}

	// Chunk the content.
	chunkOpts := &chunker.Options{
		ChunkSize:    col.ChunkSize,
		ChunkOverlap: col.ChunkOverlap,
		Strategy:     col.ChunkStrategy,
	}
	chunkResults, err := e.chunk(ctx, content, chunkOpts)
	if err != nil {
		return nil, nil, fmt.Errorf("chunk: %w", err)
	}
	span.SetAttributes(telemetry.ChunkCount.Int(len(chunkResults)))

	// Build chunk entities.
	chunks := make([]*chunk.Chunk, len(chunkResults))
	for i, cr := range chunkResults {
		chunks[i] = &chunk.Chunk{
			ID:           id.NewChunkID(),
			DocumentID:   doc.ID,
			CollectionID: doc.CollectionID,
			TenantID:     doc.TenantID,
			Content:      cr.Content,
			Index:        cr.Index,
			StartOffset:  cr.StartOffset,
			EndOffset:    cr.EndOffset,
			TokenCount:   cr.TokenCount,
			Metadata:     cr.Metadata,
			CreatedAt:    time.Now().UTC(),
		}
	}

	e.extensions.EmitIngestChunked(ctx, chunks)
	if beatErr := beat(); beatErr != nil {
		return nil, nil, beatErr
	}

	// Embed the chunks.
	texts := make([]string, len(chunks))
	for i, ch := range chunks {
		texts[i] = ch.Content
	}

	embedResults, err := e.embed(ctx, telemetry.EmbedIngest, texts)
	if err != nil {
		return nil, nil, fmt.Errorf("embed: %w", err)
	}

	e.extensions.EmitIngestEmbedded(ctx, chunks)
	if beatErr := beat(); beatErr != nil {
		return nil, nil, beatErr
	}

	// Build vector entries.
	entries := make([]vectorstore.Entry, len(chunks))
	for i, ch := range chunks {
		entries[i] = vectorstore.Entry{
			ID:       ch.ID.String(),
			Vector:   embedResults[i].Vector,
			Content:  ch.Content,
			Metadata: vectorMetadata(ch, doc.SourceKey),
		}
	}
	return chunks, entries, nil
}

// IngestBatch ingests multiple documents into a collection.
func (e *Engine) IngestBatch(ctx context.Context, inputs []*IngestInput) ([]*IngestResult, error) {
	ctx, _, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]*IngestResult, 0, len(inputs))
	for _, input := range inputs {
		result, err := e.Ingest(ctx, input)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// cleanupTimeout bounds the writes a failed ingest makes once its own
// context may already be cancelled.
const cleanupTimeout = 30 * time.Second

// cleanupContext returns a context for a failed ingest's cleanup: it keeps
// ctx's values (the tenant among them) but not its cancellation, so a
// request that gave up still gets its document marked failed, and it ends
// after cleanupTimeout.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// failIngest marks a document as failed and emits the failure event. The
// write runs on a cleanup context (see cleanupContext), so a cancelled
// request can't leave the document processing. It is the store's guarded
// FailDocument, so it never overwrites a document another attempt has made
// ready or recovery has already failed.
func (e *Engine) failIngest(ctx context.Context, doc *document.Document, colID id.CollectionID, ingestErr error) (*IngestResult, error) {
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	_ = e.store.FailDocument(cleanup, doc.ID, ingestErr.Error(), time.Time{}) //nolint:errcheck // best-effort; refused only when the document already moved on

	e.extensions.EmitIngestFailed(ctx, colID, ingestErr)

	return &IngestResult{
		DocumentID: doc.ID,
		State:      document.StateFailed,
	}, fmt.Errorf("weave: ingest failed: %w", ingestErr)
}

// endIngest ends an unkeyed ingest that stopped with err: a document that
// moved on under it (weave.ErrIngestInterrupted) is abandoned, anything else
// goes through failIngest. Either way the chunks and vectors it wrote are
// deleted afterwards (see clearAttempt), so a failed document is never
// searchable. entries are the vectors it upserted, and chunksWritten says
// whether its chunk rows may be in the store.
func (e *Engine) endIngest(ctx context.Context, doc *document.Document, entries []vectorstore.Entry, chunksWritten bool, err error) (*IngestResult, error) {
	if errors.Is(err, weave.ErrIngestInterrupted) {
		return e.abandonIngest(ctx, doc, entries, chunksWritten)
	}
	res, failErr := e.failIngest(ctx, doc, doc.CollectionID, err)
	e.clearAttempt(ctx, doc, entries, chunksWritten)
	return res, failErr
}

// abandonIngest clears what an interrupted ingest wrote and returns
// weave.ErrIngestInterrupted. The document's state is left as it is: it was
// failed, finished or deleted by someone else, and that one fires the end
// hook (recovery's IngestFailed), so this fires none and a document never
// gets two. What it wrote goes through clearAttempt. Recovery never lists a
// failed document again, so anything a failed delete leaves stays until the
// next ingest of the same content purges the row, or DeleteDocument;
// retrieval drops the hits of a failed document meanwhile.
func (e *Engine) abandonIngest(ctx context.Context, doc *document.Document, entries []vectorstore.Entry, chunksWritten bool) (*IngestResult, error) {
	e.clearAttempt(ctx, doc, entries, chunksWritten)
	return &IngestResult{DocumentID: doc.ID, State: document.StateFailed}, weave.ErrIngestInterrupted
}

// clearAttempt deletes what an unkeyed ingest of doc wrote: the vectors in
// entries by their own IDs and, when chunksWritten, the document's chunk
// rows (an unkeyed document has one attempt). It runs on a cleanup context,
// and a failure is only logged; the document's row stays.
func (e *Engine) clearAttempt(ctx context.Context, doc *document.Document, entries []vectorstore.Entry, chunksWritten bool) {
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	e.dropAttemptVectors(cleanup, entries)
	if !chunksWritten {
		return
	}
	if err := e.store.DeleteChunksByDocument(cleanup, doc.ID); err != nil {
		e.logger.Warn("failed to delete the chunks of an ingest that did not finish",
			log.String("document_id", doc.ID.String()),
			log.String("error_type", telemetry.ErrorType(err)),
		)
	}
}

// ──────────────────────────────────────────────────
// Retrieval
// ──────────────────────────────────────────────────

// RetrieveOption configures a retrieval query.
type RetrieveOption func(*RetrieveParams)

// RetrieveParams holds parsed retrieval parameters.
type RetrieveParams struct {
	CollectionID string `json:"collection_id,omitempty"`
	// TenantID is set by the deprecated WithTenantID. Retrieval always runs
	// within the context tenant; see WithTenantID.
	TenantID string  `json:"tenant_id,omitempty"`
	TopK     int     `json:"top_k"`
	MinScore float64 `json:"min_score"`
	Strategy string  `json:"strategy,omitempty"`
	// TenantFilter is set by the deprecated WithTenantFilter; see there.
	TenantFilter *string `json:"-"`
}

// WithCollection restricts retrieval to a specific collection.
func WithCollection(colID id.CollectionID) RetrieveOption {
	return func(p *RetrieveParams) { p.CollectionID = colID.String() }
}

// WithTopK sets the maximum number of results.
func WithTopK(k int) RetrieveOption {
	return func(p *RetrieveParams) { p.TopK = k }
}

// WithMinScore sets the minimum relevance score threshold.
func WithMinScore(score float64) RetrieveOption {
	return func(p *RetrieveParams) { p.MinScore = score }
}

// WithStrategy sets the retrieval strategy (e.g. "similarity", "mmr", "hybrid").
func WithStrategy(strategy string) RetrieveOption {
	return func(p *RetrieveParams) { p.Strategy = strategy }
}

// WithTenantID names the tenant to retrieve for. With no tenant on the
// context it becomes the tenant; with one, it must equal it, or Retrieve
// returns ErrInvalidArgument. "" names nothing.
//
// Deprecated: put the tenant on the context with weave.WithTenant.
func WithTenantID(tenantID string) RetrieveOption {
	return func(p *RetrieveParams) { p.TenantID = tenantID }
}

// WithTenantFilter names the tenant to retrieve for, as WithTenantID does,
// except that "" is a value: with a tenant on the context it is
// ErrInvalidArgument, and without one it is ErrTenantRequired.
//
// Deprecated: put the tenant on the context with weave.WithTenant.
func WithTenantFilter(tenant string) RetrieveOption {
	return func(p *RetrieveParams) { p.TenantFilter = &tenant }
}

// ScoredChunk is a chunk with its relevance score.
type ScoredChunk struct {
	Chunk *chunk.Chunk `json:"chunk"`
	Score float64      `json:"score"`
	// Hydrated is true when Chunk was read back from the metadata store by
	// ID, so every field on it is real.
	Hydrated bool `json:"hydrated"`
	// Orphaned is true when the vector store returned a chunk ID that has no
	// row in the metadata store. The hit keeps its rank and its text. A
	// source-keyed document's vector with no row is dropped instead, since
	// it belongs to a version not yet swapped in or already replaced.
	Orphaned bool `json:"orphaned,omitempty"`
}

// Retrieve performs a semantic retrieval query within the context tenant,
// and never returns another tenant's chunks. A collection named with
// WithCollection that the tenant doesn't have is ErrCollectionNotFound,
// before the query is embedded. Each hit is read back from the
// metadata store by chunk ID; a hit whose row is missing is kept and marked
// Orphaned, as long as its vector is labelled with the tenant and carries no
// source_key. A source-keyed hit without a row is dropped, and the hits of
// one source all come from one version of it (see oneVersionPerSource).
func (e *Engine) Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]ScoredChunk, error) {
	if e.retriever == nil && (e.embedder == nil || e.vectorStore == nil) {
		return nil, fmt.Errorf("weave: no retriever or embedder+vectorstore configured")
	}
	params := &RetrieveParams{TopK: e.config.DefaultTopK}
	for _, opt := range opts {
		opt(params)
	}
	ctx, tenant, err := e.tenantNamed(ctx, nonEmpty(params.TenantID), params.TenantFilter)
	if err != nil {
		return nil, err
	}

	colID, _ := id.ParseCollectionID(params.CollectionID) //nolint:errcheck // empty for cross-collection search
	if params.CollectionID != "" {
		if colErr := e.ownedCollections(ctx, tenant, colID); colErr != nil {
			return nil, colErr
		}
	}

	// The query text never reaches the span: it is the user's question.
	attrs := []attribute.KeyValue{
		telemetry.RetrieverKind.String(e.kinds.retriever),
		telemetry.RetrieveTopK.Int(params.TopK),
	}
	if !colID.IsNil() {
		attrs = append(attrs, telemetry.CollectionID.String(colID.String()))
	}
	ctx, span := telemetry.Tracer().Start(ctx, "retrieve chunks", trace.WithAttributes(attrs...))
	defer span.End()

	start := time.Now()
	e.extensions.EmitRetrievalStarted(ctx, colID, query)

	hits, err := e.retrieveRaw(ctx, query, params, tenant)
	if err == nil {
		hits, err = e.hydrate(ctx, tenant, hits)
	}
	if err == nil {
		hits, err = e.oneVersionPerSource(ctx, tenant, hits)
	}
	if err != nil {
		telemetry.Fail(span, err)
		span.SetAttributes(telemetry.Result.String(telemetry.ResultError))
		e.metrics.retrieve.Observe(time.Since(start).Seconds(), e.kinds.retriever, telemetry.ResultError)
		e.extensions.EmitRetrievalFailed(ctx, colID, err)
		return nil, err
	}

	// One GetChunk runs per hit, so the counts go here rather than on a
	// span per hit.
	hydrated, orphaned := 0, 0
	for i := range hits {
		if hits[i].Hydrated {
			hydrated++
		}
		if hits[i].Orphaned {
			orphaned++
		}
	}
	span.SetAttributes(
		telemetry.Result.String(telemetry.ResultOK),
		telemetry.RetrieveResults.Int(len(hits)),
		telemetry.RetrieveHydrated.Int(hydrated),
		telemetry.RetrieveOrphaned.Int(orphaned),
	)
	e.metrics.retrieve.Observe(time.Since(start).Seconds(), e.kinds.retriever, telemetry.ResultOK)
	e.metrics.orphaned.Add(float64(orphaned))
	e.extensions.EmitRetrievalCompleted(ctx, colID, len(hits), time.Since(start))
	return hits, nil
}

// nonEmpty returns &s, or nil for "".
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// searchScope builds the metadata filter and tenant key both search paths
// send to the vector side. Both always carry tenant, the resolved tenant:
// the tenant_id filter for stores that only match metadata, and the key for
// stores with their own tenant scope.
func searchScope(collectionID, tenant string) (filter map[string]string, tenantKey string) {
	filter = map[string]string{"tenant_id": tenant}
	if collectionID != "" {
		filter["collection_id"] = collectionID
	}
	return filter, tenant
}

// retrieveRaw runs the configured retriever, or a plain vector search when
// none is configured, within tenant, and returns hits as the vector side
// reported them. It emits no hooks: Retrieve does, once, around the whole
// call.
func (e *Engine) retrieveRaw(ctx context.Context, query string, params *RetrieveParams, tenant string) ([]ScoredChunk, error) {
	// A retriever that reads the store (the similarity retriever does) reads
	// within the tenant too.
	ctx = weave.WithTenant(ctx, tenant)
	filter, tenantKey := searchScope(params.CollectionID, tenant)

	if e.retriever != nil {
		results, err := e.retriever.Retrieve(ctx, query, &retriever.Options{
			CollectionID: params.CollectionID,
			TenantKey:    tenantKey,
			TopK:         params.TopK,
			MinScore:     params.MinScore,
			Filter:       filter,
		})
		if err != nil {
			return nil, fmt.Errorf("weave: retrieve: %w", err)
		}
		scored := make([]ScoredChunk, len(results))
		for i, r := range results {
			scored[i] = ScoredChunk{Chunk: r.Chunk, Score: r.Score}
		}
		return scored, nil
	}

	embedResults, err := e.embed(ctx, telemetry.EmbedQuery, []string{query})
	if err != nil {
		return nil, fmt.Errorf("weave: embed query: %w", err)
	}
	if len(embedResults) == 0 {
		return nil, fmt.Errorf("weave: embed query: no vector returned")
	}
	searchResults, err := e.searchVectors(ctx, embedResults[0].Vector, &vectorstore.SearchOptions{
		TopK:      params.TopK,
		Filter:    filter,
		TenantKey: tenantKey,
		MinScore:  params.MinScore,
	})
	if err != nil {
		return nil, fmt.Errorf("weave: search: %w", err)
	}
	scored := make([]ScoredChunk, len(searchResults))
	for i, sr := range searchResults {
		scored[i] = ScoredChunk{Chunk: retriever.ChunkFromSearchResult(sr), Score: sr.Score}
	}
	return scored, nil
}

// hydrate replaces each hit's chunk with the stored row, read within
// tenant, and drops every hit that belongs to another tenant (see
// hydrateHit). The hits slice is reused.
func (e *Engine) hydrate(ctx context.Context, tenant string, hits []ScoredChunk) ([]ScoredChunk, error) {
	kept := hits[:0]
	shown := shownDocuments{}
	for _, h := range hits {
		keep, err := e.hydrateHit(ctx, tenant, &h, shown)
		if err != nil {
			return nil, err
		}
		if keep {
			kept = append(kept, h)
		}
	}
	return kept, nil
}

// shownDocuments remembers, for one hydrate pass, whether the hits of each
// document (by ID) may be shown, so a document is read once per pass.
type shownDocuments map[string]bool

// documentShown reports whether hits of docID may be shown: the document
// is tenant's and is neither failed nor superseded. A failed document's
// chunks are normally deleted with it, so this catches what a cleanup that
// failed left behind.
func (e *Engine) documentShown(ctx context.Context, tenant string, docID id.DocumentID, shown shownDocuments) (bool, error) {
	if ok, seen := shown[docID.String()]; seen {
		return ok, nil
	}
	doc, err := e.getDocument(ctx, tenant, docID)
	switch {
	case errors.Is(err, weave.ErrDocumentNotFound):
		shown[docID.String()] = false
		return false, nil
	case err != nil:
		return false, fmt.Errorf("weave: hydrate document %s: %w", docID, err)
	}
	ok := doc.State != document.StateFailed && doc.State != document.StateSuperseded
	shown[docID.String()] = ok
	return ok, nil
}

// hydrateHit reads h's chunk back within tenant and reports whether to keep
// the hit. A hit whose document is failed, superseded or gone is dropped
// (see documentShown). A hit whose vector is labelled with another tenant is dropped,
// and so is a missing row whose vector isn't labelled with tenant, so an
// Orphaned hit never carries another tenant's text. The built-in stores
// never return either; a custom vector store or retriever that ignores the
// tenant filter can. A missing row whose vector carries a source_key is
// dropped as well (see the case below). A hit with no chunk, or no chunk
// ID (a custom retriever that sets none), is otherwise left as it is,
// unhydrated.
func (e *Engine) hydrateHit(ctx context.Context, tenant string, h *ScoredChunk, shown shownDocuments) (bool, error) {
	if h.Chunk == nil {
		return true, nil
	}
	label, labelled := h.Chunk.Metadata["tenant_id"]
	if labelled && label != tenant || h.Chunk.TenantID != "" && h.Chunk.TenantID != tenant {
		return false, nil
	}
	if e.store == nil || h.Chunk.ID.String() == "" {
		return true, nil
	}
	row, err := e.store.GetChunk(ctx, h.Chunk.ID)
	if err == nil {
		row, err = ownChunk(row, tenant)
	}
	switch {
	case errors.Is(err, weave.ErrChunkNotFound):
		// A source-keyed vector with no row is a version that isn't swapped
		// in yet, or one already replaced, so it is never shown.
		if label != tenant || h.Chunk.Metadata["source_key"] != "" {
			return false, nil
		}
		h.Orphaned = true
	case err != nil:
		return false, fmt.Errorf("weave: hydrate chunk %s: %w", h.Chunk.ID, err)
	default:
		ok, docErr := e.documentShown(ctx, tenant, row.DocumentID, shown)
		if docErr != nil || !ok {
			return false, docErr
		}
		h.Chunk = hydratedCopy(row, h.Chunk.Metadata)
		h.Hydrated = true
	}
	return true, nil
}

// hydratedCopy returns a copy of the stored row, so a caller that edits a
// hit never edits the store's own chunk. Its metadata keeps the vector-side
// keys that clients of the retrieve route already read, with the row's own
// keys winning a clash, except that collection_id, document_id, tenant_id
// and chunk_index always come from the row's own fields.
func hydratedCopy(row *chunk.Chunk, vectorMeta map[string]string) *chunk.Chunk {
	out := *row
	out.Metadata = make(map[string]string, len(vectorMeta)+len(row.Metadata))
	maps.Copy(out.Metadata, vectorMeta)
	maps.Copy(out.Metadata, row.Metadata)
	maps.Copy(out.Metadata, vectorMetadata(&chunk.Chunk{
		CollectionID: row.CollectionID, DocumentID: row.DocumentID, TenantID: row.TenantID, Index: row.Index,
	}, vectorMeta["source_key"]))
	if vectorMeta["source_key"] == "" {
		delete(out.Metadata, "source_key")
	}
	return &out
}

// ──────────────────────────────────────────────────
// Document operations
// ──────────────────────────────────────────────────

// GetDocument retrieves a document by ID within the context tenant.
func (e *Engine) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	return e.getDocument(ctx, tenant, docID)
}

// getDocument reads a document and checks it belongs to tenant.
func (e *Engine) getDocument(ctx context.Context, tenant string, docID id.DocumentID) (*document.Document, error) {
	doc, err := e.store.GetDocument(ctx, docID)
	if err != nil {
		return nil, err
	}
	return ownDocument(doc, tenant)
}

// ListDocuments returns the context tenant's documents matching the given
// filter.
func (e *Engine) ListDocuments(ctx context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	docs, err := e.store.ListDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}
	return owned(docs, tenant, documentTenant), nil
}

// DeleteDocument removes a document and its chunks from both stores.
// Another tenant's document is ErrDocumentNotFound, and nothing is deleted.
func (e *Engine) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	if e.store == nil {
		return weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return err
	}
	if _, err = e.getDocument(ctx, tenant, docID); err != nil {
		return err
	}

	// Delete vector entries for the document.
	if e.vectorStore != nil {
		if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
			"document_id": docID.String(),
			"tenant_id":   tenant,
		}); err != nil {
			// The error's text can quote a DSN, a host or a tenant key, so
			// only its type is logged.
			e.logger.Warn("failed to delete vector entries for document",
				log.String("document_id", docID.String()),
				log.String("error_type", telemetry.ErrorType(err)),
			)
		}
	}

	// Delete chunks.
	if err := e.store.DeleteChunksByDocument(ctx, docID); err != nil {
		return fmt.Errorf("weave: delete chunks: %w", err)
	}

	// Delete the document.
	if err := e.store.DeleteDocument(ctx, docID); err != nil {
		return err
	}

	e.extensions.EmitDocumentDeleted(ctx, docID)
	return nil
}

// ──────────────────────────────────────────────────
// Reindex
// ──────────────────────────────────────────────────

// ReindexCollection re-embeds the chunks of every ready document in a
// collection and replaces each document's vectors in turn. Documents that
// aren't ready (an ingest still in flight, a failed one) and their vectors
// are left alone.
func (e *Engine) ReindexCollection(ctx context.Context, colID id.CollectionID) error {
	if e.store == nil {
		return weave.ErrNoStore
	}
	if e.embedder == nil {
		return weave.ErrNoEmbedder
	}
	if e.vectorStore == nil {
		return weave.ErrNoVectorStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return err
	}

	ctx, span := telemetry.Tracer().Start(ctx, "reindex collection", trace.WithAttributes(
		telemetry.CollectionID.String(colID.String()),
		telemetry.VectorStoreKind.String(e.kinds.vectorStore),
	))
	defer span.End()

	docs, chunks, err := e.reindex(ctx, tenant, colID)
	span.SetAttributes(
		telemetry.ReindexDocuments.Int(docs),
		telemetry.ReindexChunks.Int(chunks),
	)
	if err != nil {
		telemetry.Fail(span, err)
	}
	return err
}

// reindex is ReindexCollection's body, run under its span for tenant. It
// returns how many documents and chunks it re-embedded, so far when it
// fails. Another tenant's collection is ErrCollectionNotFound before
// anything is deleted or embedded.
//
// It works one ready document at a time: embed its chunks, delete that
// document's vectors, upsert the new ones. Nothing else in the collection
// is touched, so a keyed version that has written its vectors and not yet
// swapped in keeps them. A document that is no longer ready when its turn
// comes is skipped; its own ingest, or recovery, finishes it.
func (e *Engine) reindex(ctx context.Context, tenant string, colID id.CollectionID) (docCount, chunkCount int, err error) {
	if _, err = e.getCollection(ctx, tenant, colID); err != nil {
		return 0, 0, err
	}
	start := time.Now()
	e.extensions.EmitReindexStarted(ctx, colID)

	docs, err := e.store.ListDocuments(ctx, &document.ListFilter{
		CollectionID: colID,
		State:        document.StateReady,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("weave: list documents for reindex: %w", err)
	}
	docs = owned(docs, tenant, documentTenant)

	for _, doc := range docs {
		n, docErr := e.reindexDocument(ctx, tenant, doc.ID)
		if docErr != nil {
			return docCount, chunkCount, docErr
		}
		if n > 0 {
			docCount++
			chunkCount += n
		}
	}

	elapsed := time.Since(start)
	e.extensions.EmitReindexCompleted(ctx, colID, elapsed)
	return docCount, chunkCount, nil
}

// reindexDocument re-embeds one document of tenant and replaces its
// vectors, and returns how many chunks it re-embedded. A document that is
// gone or no longer ready, or has no chunks, is skipped with 0. The
// document's vectors are deleted only once its new ones are embedded, right
// before they are upserted, so a failed embed leaves the old ones in place.
func (e *Engine) reindexDocument(ctx context.Context, tenant string, docID id.DocumentID) (int, error) {
	doc, err := e.getDocument(ctx, tenant, docID)
	switch {
	case errors.Is(err, weave.ErrDocumentNotFound):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("weave: read document for reindex: %w", err)
	case doc.State != document.StateReady:
		return 0, nil
	}

	chunks, err := e.store.ListChunksByDocument(ctx, doc.ID)
	if err != nil {
		return 0, fmt.Errorf("weave: list chunks for reindex: %w", err)
	}
	chunks = owned(chunks, tenant, chunkTenant)
	if len(chunks) == 0 {
		return 0, nil
	}

	texts := make([]string, len(chunks))
	for i, ch := range chunks {
		texts[i] = ch.Content
	}
	embedResults, err := e.embed(ctx, telemetry.EmbedReindex, texts)
	if err != nil {
		return 0, fmt.Errorf("weave: embed for reindex: %w", err)
	}

	entries := make([]vectorstore.Entry, len(chunks))
	for i, ch := range chunks {
		entries[i] = vectorstore.Entry{
			ID:       ch.ID.String(),
			Vector:   embedResults[i].Vector,
			Content:  ch.Content,
			Metadata: vectorMetadata(ch, doc.SourceKey),
		}
	}

	if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
		"document_id": doc.ID.String(),
		"tenant_id":   tenant,
	}); err != nil {
		return 0, fmt.Errorf("weave: delete vectors for reindex: %w", err)
	}
	if err := e.upsertVectors(ctx, entries); err != nil {
		return 0, fmt.Errorf("weave: upsert for reindex: %w", err)
	}
	return len(chunks), nil
}

// ──────────────────────────────────────────────────
// Hybrid Search
// ──────────────────────────────────────────────────

// HybridSearchParams configures a hybrid search across multiple collections.
type HybridSearchParams struct {
	// Collections restricts search to these collection IDs.
	Collections []id.CollectionID `json:"collections,omitempty"`
	// TopK is the maximum number of results.
	TopK int `json:"top_k"`
	// Strategy is the search strategy name.
	Strategy string `json:"strategy,omitempty"`
	// MinScore is the minimum relevance score threshold.
	MinScore float64 `json:"min_score"`
}

// HybridSearch performs retrieval across one or more of the context
// tenant's collections, or all of them when none is named. A named
// collection the tenant doesn't have is ErrCollectionNotFound, before
// anything is embedded.
func (e *Engine) HybridSearch(ctx context.Context, query string, params *HybridSearchParams) ([]ScoredChunk, error) {
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	if params != nil {
		// Every named collection is checked before any of them is searched,
		// so a bad one is refused before the query is embedded at all.
		if colErr := e.ownedCollections(ctx, tenant, params.Collections...); colErr != nil {
			return nil, colErr
		}
	}
	if params == nil {
		params = &HybridSearchParams{}
	}
	if params.TopK == 0 {
		params.TopK = e.config.DefaultTopK
	}

	// If specific collections are given, search each and merge.
	if len(params.Collections) > 0 {
		all := []ScoredChunk{}
		for _, colID := range params.Collections {
			results, err := e.Retrieve(ctx, query,
				WithCollection(colID),
				WithTopK(params.TopK),
				WithMinScore(params.MinScore),
				WithStrategy(params.Strategy),
			)
			if err != nil {
				return nil, err
			}
			all = append(all, results...)
		}

		// Sort by score descending and limit to TopK.
		sortScoredChunks(all)
		if len(all) > params.TopK {
			all = all[:params.TopK]
		}
		return all, nil
	}

	// No collection filter: search every collection of the tenant.
	return e.Retrieve(ctx, query,
		WithTopK(params.TopK),
		WithMinScore(params.MinScore),
		WithStrategy(params.Strategy),
	)
}

// sortScoredChunks sorts scored chunks by score descending.
func sortScoredChunks(chunks []ScoredChunk) {
	for i := 1; i < len(chunks); i++ {
		for j := i; j > 0 && chunks[j].Score > chunks[j-1].Score; j-- {
			chunks[j], chunks[j-1] = chunks[j-1], chunks[j]
		}
	}
}
