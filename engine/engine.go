package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"strings"
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
}

// New creates a new Engine with the given options.
func New(opts ...Option) (*Engine, error) {
	e := &Engine{
		config: weave.DefaultConfig(),
		logger: log.NewNoopLogger(),
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

// Start initialises the engine. Currently a no-op but reserved for
// future background processes (e.g. ingest workers).
func (e *Engine) Start(_ context.Context) error {
	return nil
}

// Stop gracefully shuts down the engine.
func (e *Engine) Stop(ctx context.Context) error {
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
	if createErr := e.store.CreateDocument(ctx, doc); createErr != nil {
		return nil, fmt.Errorf("weave: create document: %w", createErr)
	}

	e.extensions.EmitIngestStarted(ctx, input.CollectionID, []*document.Document{doc})

	// Mark processing.
	doc.State = document.StateProcessing
	_ = e.store.UpdateDocument(ctx, doc) //nolint:errcheck // best-effort status update

	chunks, entries, err := e.embedChunks(ctx, span, col, doc, input)
	if err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, err)
	}

	// Store chunks in metadata store.
	if err := e.store.CreateChunkBatch(ctx, chunks); err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("store chunks: %w", err))
	}

	// Upsert vector entries.
	if err := e.upsertVectors(ctx, entries); err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("upsert vectors: %w", err))
	}

	// Mark document as ready.
	doc.State = document.StateReady
	doc.ChunkCount = len(chunks)
	_ = e.store.UpdateDocument(ctx, doc) //nolint:errcheck // best-effort status update

	elapsed := time.Since(start)
	e.extensions.EmitIngestCompleted(ctx, input.CollectionID, 1, len(chunks), elapsed)

	return &IngestResult{
		DocumentID: doc.ID,
		ChunkCount: len(chunks),
		State:      document.StateReady,
	}, nil
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
// chunked and embedded hooks. Its error is ready for failIngest.
func (e *Engine) embedChunks(ctx context.Context, span trace.Span, col *collection.Collection, doc *document.Document, input *IngestInput) ([]*chunk.Chunk, []vectorstore.Entry, error) {
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

// failIngest marks a document as failed and emits the failure event. It
// writes a copy of doc, so a store that keeps the pointer it was handed
// (the memory store does) never sees the caller's value change under it.
func (e *Engine) failIngest(ctx context.Context, doc *document.Document, colID id.CollectionID, ingestErr error) (*IngestResult, error) {
	failed := *doc
	failed.State = document.StateFailed
	failed.Error = ingestErr.Error()
	_ = e.store.UpdateDocument(ctx, &failed) //nolint:errcheck // best-effort status update

	e.extensions.EmitIngestFailed(ctx, colID, ingestErr)

	return &IngestResult{
		DocumentID: doc.ID,
		State:      document.StateFailed,
	}, fmt.Errorf("weave: ingest failed: %w", ingestErr)
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
// source_key. A source-keyed hit without a row is dropped.
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
	for _, h := range hits {
		keep, err := e.hydrateHit(ctx, tenant, &h)
		if err != nil {
			return nil, err
		}
		if keep {
			kept = append(kept, h)
		}
	}
	return kept, nil
}

// hydrateHit reads h's chunk back within tenant and reports whether to keep
// the hit. A hit whose vector is labelled with another tenant is dropped,
// and so is a missing row whose vector isn't labelled with tenant, so an
// Orphaned hit never carries another tenant's text. A missing row whose
// vector carries a source_key is dropped as well (see the case below). The built-in stores
// never return either; a custom vector store or retriever that ignores the
// tenant filter can. A hit with no chunk, or no chunk ID (a custom
// retriever that sets none), is otherwise left as it is, unhydrated.
func (e *Engine) hydrateHit(ctx context.Context, tenant string, h *ScoredChunk) (bool, error) {
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
		// in yet, or one already replaced, so it is never shown: Retrieve
		// never returns two versions of one source.
		if label != tenant || h.Chunk.Metadata["source_key"] != "" {
			return false, nil
		}
		h.Orphaned = true
	case err != nil:
		return false, fmt.Errorf("weave: hydrate chunk %s: %w", h.Chunk.ID, err)
	default:
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

// ReindexCollection re-embeds and re-stores all chunks in a collection.
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
func (e *Engine) reindex(ctx context.Context, tenant string, colID id.CollectionID) (docCount, chunkCount int, err error) {
	if _, err = e.getCollection(ctx, tenant, colID); err != nil {
		return 0, 0, err
	}
	start := time.Now()
	e.extensions.EmitReindexStarted(ctx, colID)

	// List all documents in the collection.
	docs, err := e.store.ListDocuments(ctx, &document.ListFilter{
		CollectionID: colID,
		State:        document.StateReady,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("weave: list documents for reindex: %w", err)
	}
	docs = owned(docs, tenant, documentTenant)

	// Delete existing vector entries for the collection.
	if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
		"collection_id": colID.String(),
		"tenant_id":     tenant,
	}); err != nil {
		return 0, 0, fmt.Errorf("weave: delete vectors for reindex: %w", err)
	}

	// Re-embed each document's chunks.
	for _, doc := range docs {
		chunks, err := e.store.ListChunksByDocument(ctx, doc.ID)
		if err != nil {
			return docCount, chunkCount, fmt.Errorf("weave: list chunks for reindex: %w", err)
		}
		chunks = owned(chunks, tenant, chunkTenant)

		if len(chunks) == 0 {
			continue
		}

		texts := make([]string, len(chunks))
		for i, ch := range chunks {
			texts[i] = ch.Content
		}

		embedResults, err := e.embed(ctx, telemetry.EmbedReindex, texts)
		if err != nil {
			return docCount, chunkCount, fmt.Errorf("weave: embed for reindex: %w", err)
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

		if err := e.upsertVectors(ctx, entries); err != nil {
			return docCount, chunkCount, fmt.Errorf("weave: upsert for reindex: %w", err)
		}
		docCount++
		chunkCount += len(chunks)
	}

	elapsed := time.Since(start)
	e.extensions.EmitReindexCompleted(ctx, colID, elapsed)
	return docCount, chunkCount, nil
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
