package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/loader"
	"github.com/xraph/weave/plugins"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/vectorstore"
)

// Engine is the central coordinator for the Weave RAG pipeline.
type Engine struct {
	config      weave.Config
	logger      *slog.Logger
	store       store.Store
	vectorStore vectorstore.VectorStore
	embedder    embedder.Embedder
	chunker     chunker.Chunker
	loader      loader.Loader
	retriever   retriever.Retriever
	extensions  *plugins.Registry
	pendingExts []plugins.Extension
}

// New creates a new Engine with the given options.
func New(opts ...Option) (*Engine, error) {
	e := &Engine{
		config: weave.DefaultConfig(),
		logger: slog.Default(),
	}
	for _, opt := range opts {
		if err := opt(e); err != nil {
			return nil, fmt.Errorf("weave: apply option: %w", err)
		}
	}

	// Wire up the extension registry.
	e.extensions = plugins.NewRegistry(e.logger)
	for _, extension := range e.pendingExts {
		e.extensions.Register(extension)
	}
	e.pendingExts = nil

	return e, nil
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
func (e *Engine) Logger() *slog.Logger { return e.logger }

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

	if col.ID.String() == "" {
		col.ID = id.NewCollectionID()
	}
	if col.TenantID == "" {
		col.TenantID = weave.TenantFromContext(ctx)
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

// GetCollection retrieves a collection by ID.
func (e *Engine) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	return e.store.GetCollection(ctx, colID)
}

// GetCollectionByName retrieves a collection by tenant and name.
func (e *Engine) GetCollectionByName(ctx context.Context, name string) (*collection.Collection, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	tenantID := weave.TenantFromContext(ctx)
	return e.store.GetCollectionByName(ctx, tenantID, name)
}

// ListCollections returns collections matching the given filter.
func (e *Engine) ListCollections(ctx context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	return e.store.ListCollections(ctx, filter)
}

// DeleteCollection removes a collection and all its documents and chunks.
func (e *Engine) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	if e.store == nil {
		return weave.ErrNoStore
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
		}); err != nil {
			e.logger.Warn("failed to delete vector entries for collection",
				slog.String("collection_id", colID.String()),
				slog.String("error", err.Error()),
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

	col, err := e.store.GetCollection(ctx, colID)
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
}

// IngestResult contains the outcome of a document ingestion.
type IngestResult struct {
	DocumentID id.DocumentID  `json:"document_id"`
	ChunkCount int            `json:"chunk_count"`
	State      document.State `json:"state"`
}

// Ingest processes a single document: load, chunk, embed, and store.
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
	if input.Content == "" {
		return nil, weave.ErrEmptyContent
	}

	start := time.Now()
	tenantID := weave.TenantFromContext(ctx)

	// Verify collection exists.
	col, err := e.store.GetCollection(ctx, input.CollectionID)
	if err != nil {
		return nil, err
	}

	// Compute content hash.
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(input.Content)))

	// Create the document record.
	doc := &document.Document{
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
		State:         document.StatePending,
	}

	if createErr := e.store.CreateDocument(ctx, doc); createErr != nil {
		return nil, fmt.Errorf("weave: create document: %w", createErr)
	}

	e.extensions.EmitIngestStarted(ctx, input.CollectionID, []*document.Document{doc})

	// Mark processing.
	doc.State = document.StateProcessing
	_ = e.store.UpdateDocument(ctx, doc) //nolint:errcheck // best-effort status update

	// Optionally load/extract text.
	content := input.Content
	if e.loader != nil && input.SourceType != "" && e.loader.Supports(input.SourceType) {
		result, loadErr := e.loader.Load(ctx, strings.NewReader(content))
		if loadErr != nil {
			return e.failIngest(ctx, doc, input.CollectionID, loadErr)
		}
		content = result.Content
	}

	// Chunk the content.
	chunkOpts := &chunker.Options{
		ChunkSize:    col.ChunkSize,
		ChunkOverlap: col.ChunkOverlap,
		Strategy:     col.ChunkStrategy,
	}
	chunkResults, err := e.chunker.Chunk(ctx, content, chunkOpts)
	if err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("chunk: %w", err))
	}

	// Build chunk entities.
	chunks := make([]*chunk.Chunk, len(chunkResults))
	for i, cr := range chunkResults {
		chunks[i] = &chunk.Chunk{
			ID:           id.NewChunkID(),
			DocumentID:   doc.ID,
			CollectionID: input.CollectionID,
			TenantID:     tenantID,
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

	embedResults, err := e.embedder.Embed(ctx, texts)
	if err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("embed: %w", err))
	}

	e.extensions.EmitIngestEmbedded(ctx, chunks)

	// Build vector entries.
	entries := make([]vectorstore.Entry, len(chunks))
	for i, ch := range chunks {
		meta := map[string]string{
			"collection_id": input.CollectionID.String(),
			"document_id":   doc.ID.String(),
			"tenant_id":     tenantID,
			"chunk_index":   fmt.Sprintf("%d", ch.Index),
		}
		for k, v := range ch.Metadata {
			meta[k] = v
		}

		entries[i] = vectorstore.Entry{
			ID:       ch.ID.String(),
			Vector:   embedResults[i].Vector,
			Content:  ch.Content,
			Metadata: meta,
		}
	}

	// Store chunks in metadata store.
	if err := e.store.CreateChunkBatch(ctx, chunks); err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("store chunks: %w", err))
	}

	// Upsert vector entries.
	if err := e.vectorStore.Upsert(ctx, entries); err != nil {
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

// IngestBatch ingests multiple documents into a collection.
func (e *Engine) IngestBatch(ctx context.Context, inputs []*IngestInput) ([]*IngestResult, error) {
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

// failIngest marks a document as failed and emits the failure event.
func (e *Engine) failIngest(ctx context.Context, doc *document.Document, colID id.CollectionID, ingestErr error) (*IngestResult, error) {
	doc.State = document.StateFailed
	doc.Error = ingestErr.Error()
	_ = e.store.UpdateDocument(ctx, doc) //nolint:errcheck // best-effort status update

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
	CollectionID string  `json:"collection_id,omitempty"`
	TenantID     string  `json:"tenant_id,omitempty"`
	TopK         int     `json:"top_k"`
	MinScore     float64 `json:"min_score"`
	Strategy     string  `json:"strategy,omitempty"`
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

// WithTenantID explicitly sets the tenant for retrieval.
func WithTenantID(tenantID string) RetrieveOption {
	return func(p *RetrieveParams) { p.TenantID = tenantID }
}

// ScoredChunk is a chunk with its relevance score.
type ScoredChunk struct {
	Chunk *chunk.Chunk `json:"chunk"`
	Score float64      `json:"score"`
}

// Retrieve performs a semantic retrieval query.
func (e *Engine) Retrieve(ctx context.Context, query string, opts ...RetrieveOption) ([]ScoredChunk, error) {
	if e.retriever == nil && (e.embedder == nil || e.vectorStore == nil) {
		return nil, fmt.Errorf("weave: no retriever or embedder+vectorstore configured")
	}

	params := &RetrieveParams{
		TopK: e.config.DefaultTopK,
	}
	for _, opt := range opts {
		opt(params)
	}

	// Resolve tenant from context if not explicitly set.
	if params.TenantID == "" {
		params.TenantID = weave.TenantFromContext(ctx)
	}

	colID, _ := id.ParseCollectionID(params.CollectionID) //nolint:errcheck // collection ID may be empty for cross-collection search
	start := time.Now()

	e.extensions.EmitRetrievalStarted(ctx, colID, query)

	// Use the plugged-in retriever if available.
	if e.retriever != nil {
		filter := map[string]string{}
		if params.CollectionID != "" {
			filter["collection_id"] = params.CollectionID
		}
		if params.TenantID != "" {
			filter["tenant_id"] = params.TenantID
		}

		results, err := e.retriever.Retrieve(ctx, query, &retriever.Options{
			CollectionID: params.CollectionID,
			TenantKey:    params.TenantID,
			TopK:         params.TopK,
			MinScore:     params.MinScore,
			Filter:       filter,
		})
		if err != nil {
			e.extensions.EmitRetrievalFailed(ctx, colID, err)
			return nil, fmt.Errorf("weave: retrieve: %w", err)
		}

		scored := make([]ScoredChunk, len(results))
		for i, r := range results {
			scored[i] = ScoredChunk{Chunk: r.Chunk, Score: r.Score}
		}

		elapsed := time.Since(start)
		e.extensions.EmitRetrievalCompleted(ctx, colID, len(scored), elapsed)
		return scored, nil
	}

	// Fallback: embed query and search vector store directly.
	embedResults, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		e.extensions.EmitRetrievalFailed(ctx, colID, err)
		return nil, fmt.Errorf("weave: embed query: %w", err)
	}

	searchFilter := map[string]string{}
	if params.CollectionID != "" {
		searchFilter["collection_id"] = params.CollectionID
	}
	if params.TenantID != "" {
		searchFilter["tenant_id"] = params.TenantID
	}

	searchResults, err := e.vectorStore.Search(ctx, embedResults[0].Vector, &vectorstore.SearchOptions{
		TopK:      params.TopK,
		Filter:    searchFilter,
		TenantKey: params.TenantID,
		MinScore:  params.MinScore,
	})
	if err != nil {
		e.extensions.EmitRetrievalFailed(ctx, colID, err)
		return nil, fmt.Errorf("weave: search: %w", err)
	}

	scored := make([]ScoredChunk, len(searchResults))
	for i, sr := range searchResults {
		scored[i] = ScoredChunk{
			Chunk: &chunk.Chunk{
				Content:  sr.Content,
				Metadata: sr.Metadata,
			},
			Score: sr.Score,
		}
	}

	elapsed := time.Since(start)
	e.extensions.EmitRetrievalCompleted(ctx, colID, len(scored), elapsed)
	return scored, nil
}

// ──────────────────────────────────────────────────
// Document operations
// ──────────────────────────────────────────────────

// GetDocument retrieves a document by ID.
func (e *Engine) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	return e.store.GetDocument(ctx, docID)
}

// ListDocuments returns documents matching the given filter.
func (e *Engine) ListDocuments(ctx context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	return e.store.ListDocuments(ctx, filter)
}

// DeleteDocument removes a document and its chunks from both stores.
func (e *Engine) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	if e.store == nil {
		return weave.ErrNoStore
	}

	// Delete vector entries for the document.
	if e.vectorStore != nil {
		if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
			"document_id": docID.String(),
		}); err != nil {
			e.logger.Warn("failed to delete vector entries for document",
				slog.String("document_id", docID.String()),
				slog.String("error", err.Error()),
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

	start := time.Now()
	e.extensions.EmitReindexStarted(ctx, colID)

	// List all documents in the collection.
	docs, err := e.store.ListDocuments(ctx, &document.ListFilter{
		CollectionID: colID,
		State:        document.StateReady,
	})
	if err != nil {
		return fmt.Errorf("weave: list documents for reindex: %w", err)
	}

	// Delete existing vector entries for the collection.
	if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
		"collection_id": colID.String(),
	}); err != nil {
		return fmt.Errorf("weave: delete vectors for reindex: %w", err)
	}

	// Re-embed each document's chunks.
	for _, doc := range docs {
		chunks, err := e.store.ListChunksByDocument(ctx, doc.ID)
		if err != nil {
			return fmt.Errorf("weave: list chunks for reindex: %w", err)
		}

		if len(chunks) == 0 {
			continue
		}

		texts := make([]string, len(chunks))
		for i, ch := range chunks {
			texts[i] = ch.Content
		}

		embedResults, err := e.embedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("weave: embed for reindex: %w", err)
		}

		entries := make([]vectorstore.Entry, len(chunks))
		for i, ch := range chunks {
			meta := map[string]string{
				"collection_id": colID.String(),
				"document_id":   doc.ID.String(),
				"tenant_id":     ch.TenantID,
				"chunk_index":   fmt.Sprintf("%d", ch.Index),
			}
			for k, v := range ch.Metadata {
				meta[k] = v
			}
			entries[i] = vectorstore.Entry{
				ID:       ch.ID.String(),
				Vector:   embedResults[i].Vector,
				Content:  ch.Content,
				Metadata: meta,
			}
		}

		if err := e.vectorStore.Upsert(ctx, entries); err != nil {
			return fmt.Errorf("weave: upsert for reindex: %w", err)
		}
	}

	elapsed := time.Since(start)
	e.extensions.EmitReindexCompleted(ctx, colID, elapsed)
	return nil
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

// HybridSearch performs retrieval across one or more collections.
func (e *Engine) HybridSearch(ctx context.Context, query string, params *HybridSearchParams) ([]ScoredChunk, error) {
	if params == nil {
		params = &HybridSearchParams{}
	}
	if params.TopK == 0 {
		params.TopK = e.config.DefaultTopK
	}

	// If specific collections are given, search each and merge.
	if len(params.Collections) > 0 {
		var all []ScoredChunk
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

	// No collection filter — search across all.
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
