package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/xraph/weave/assembler"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/loader"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/vectorstore"
)

// Context keys for passing data between steps.
const (
	KeyContent      = "content"       // string — loaded text content
	KeyChunks       = "chunks"        // []chunker.ChunkResult
	KeyEmbeddings   = "embeddings"    // []embedder.EmbedResult
	KeyEntries      = "entries"       // []vectorstore.Entry
	KeyQuery        = "query"         // string — retrieval query
	KeyResults      = "results"       // []retriever.Result
	KeyAssembled    = "assembled"     // *assembler.AssembleResult
	KeyCollectionID = "collection_id" // string
	KeyDocumentID   = "document_id"   // string
	KeyTenantID     = "tenant_id"     // string
)

// ──────────────────────────────────────────────────
// LoadStep
// ──────────────────────────────────────────────────

// LoadStep extracts text content using a Loader.
// Input: KeyContent (io.Reader via raw content string)
// Output: KeyContent (extracted text)
type LoadStep struct {
	loader loader.Loader
}

// NewLoadStep creates a load step.
func NewLoadStep(l loader.Loader) *LoadStep {
	return &LoadStep{loader: l}
}

// Name returns "load".
func (s *LoadStep) Name() string { return "load" }

// Run extracts text from the content in the step context.
func (s *LoadStep) Run(ctx context.Context, sc *StepContext) error {
	raw, ok := sc.Get(KeyContent)
	if !ok {
		return fmt.Errorf("load: missing content in context")
	}

	content, ok := raw.(string)
	if !ok {
		return fmt.Errorf("load: content is not a string")
	}

	result, err := s.loader.Load(ctx, strings.NewReader(content))
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}

	sc.Set(KeyContent, result.Content)
	return nil
}

// ──────────────────────────────────────────────────
// ChunkStep
// ──────────────────────────────────────────────────

// ChunkStep splits text into chunks.
// Input: KeyContent (string)
// Output: KeyChunks ([]chunker.ChunkResult)
type ChunkStep struct {
	chunker chunker.Chunker
	opts    *chunker.Options
}

// NewChunkStep creates a chunk step.
func NewChunkStep(c chunker.Chunker, opts *chunker.Options) *ChunkStep {
	return &ChunkStep{chunker: c, opts: opts}
}

// Name returns "chunk".
func (s *ChunkStep) Name() string { return "chunk" }

// Run chunks the text content.
func (s *ChunkStep) Run(ctx context.Context, sc *StepContext) error {
	content := sc.MustGet(KeyContent).(string)

	chunks, err := s.chunker.Chunk(ctx, content, s.opts)
	if err != nil {
		return fmt.Errorf("chunk: %w", err)
	}

	sc.Set(KeyChunks, chunks)
	return nil
}

// ──────────────────────────────────────────────────
// EmbedStep
// ──────────────────────────────────────────────────

// EmbedStep generates embeddings for chunks.
// Input: KeyChunks ([]chunker.ChunkResult)
// Output: KeyEmbeddings ([]embedder.EmbedResult), KeyEntries ([]vectorstore.Entry)
type EmbedStep struct {
	embedder embedder.Embedder
}

// NewEmbedStep creates an embed step.
func NewEmbedStep(e embedder.Embedder) *EmbedStep {
	return &EmbedStep{embedder: e}
}

// Name returns "embed".
func (s *EmbedStep) Name() string { return "embed" }

// Run generates embeddings.
func (s *EmbedStep) Run(ctx context.Context, sc *StepContext) error {
	chunks := sc.MustGet(KeyChunks).([]chunker.ChunkResult)

	texts := make([]string, len(chunks))
	for i, ch := range chunks {
		texts[i] = ch.Content
	}

	results, err := s.embedder.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	sc.Set(KeyEmbeddings, results)

	// Build vector store entries.
	collectionID, _ := sc.Get(KeyCollectionID)
	documentID, _ := sc.Get(KeyDocumentID)
	tenantID, _ := sc.Get(KeyTenantID)

	entries := make([]vectorstore.Entry, len(chunks))
	for i, ch := range chunks {
		chunkID := id.NewChunkID()
		meta := map[string]string{
			"chunk_index": fmt.Sprintf("%d", ch.Index),
		}
		if cid, ok := collectionID.(string); ok && cid != "" {
			meta["collection_id"] = cid
		}
		if did, ok := documentID.(string); ok && did != "" {
			meta["document_id"] = did
		}
		if tid, ok := tenantID.(string); ok && tid != "" {
			meta["tenant_id"] = tid
		}

		entries[i] = vectorstore.Entry{
			ID:       chunkID.String(),
			Vector:   results[i].Vector,
			Content:  ch.Content,
			Metadata: meta,
		}
	}

	sc.Set(KeyEntries, entries)
	return nil
}

// ──────────────────────────────────────────────────
// StoreStep
// ──────────────────────────────────────────────────

// StoreStep persists vector entries to the vector store.
// Input: KeyEntries ([]vectorstore.Entry)
type StoreStep struct {
	vs vectorstore.VectorStore
}

// NewStoreStep creates a store step.
func NewStoreStep(vs vectorstore.VectorStore) *StoreStep {
	return &StoreStep{vs: vs}
}

// Name returns "store".
func (s *StoreStep) Name() string { return "store" }

// Run upserts entries to the vector store.
func (s *StoreStep) Run(ctx context.Context, sc *StepContext) error {
	entries := sc.MustGet(KeyEntries).([]vectorstore.Entry)
	return s.vs.Upsert(ctx, entries)
}

// ──────────────────────────────────────────────────
// RetrieveStep
// ──────────────────────────────────────────────────

// RetrieveStep retrieves relevant chunks for a query.
// Input: KeyQuery (string)
// Output: KeyResults ([]retriever.Result)
type RetrieveStep struct {
	retriever retriever.Retriever
	opts      *retriever.Options
}

// NewRetrieveStep creates a retrieve step.
func NewRetrieveStep(r retriever.Retriever, opts *retriever.Options) *RetrieveStep {
	return &RetrieveStep{retriever: r, opts: opts}
}

// Name returns "retrieve".
func (s *RetrieveStep) Name() string { return "retrieve" }

// Run retrieves relevant chunks.
func (s *RetrieveStep) Run(ctx context.Context, sc *StepContext) error {
	query := sc.MustGet(KeyQuery).(string)

	results, err := s.retriever.Retrieve(ctx, query, s.opts)
	if err != nil {
		return fmt.Errorf("retrieve: %w", err)
	}

	sc.Set(KeyResults, results)
	return nil
}

// ──────────────────────────────────────────────────
// AssembleStep
// ──────────────────────────────────────────────────

// AssembleStep builds context from retrieved results.
// Input: KeyResults ([]retriever.Result)
// Output: KeyAssembled (*assembler.AssembleResult)
type AssembleStep struct {
	assembler *assembler.Assembler
}

// NewAssembleStep creates an assemble step.
func NewAssembleStep(a *assembler.Assembler) *AssembleStep {
	return &AssembleStep{assembler: a}
}

// Name returns "assemble".
func (s *AssembleStep) Name() string { return "assemble" }

// Run assembles context from retrieval results.
func (s *AssembleStep) Run(ctx context.Context, sc *StepContext) error {
	results := sc.MustGet(KeyResults).([]retriever.Result)

	assembled, err := s.assembler.Assemble(ctx, results)
	if err != nil {
		return fmt.Errorf("assemble: %w", err)
	}

	sc.Set(KeyAssembled, assembled)
	return nil
}

// ──────────────────────────────────────────────────
// FuncStep
// ──────────────────────────────────────────────────

// FuncStep wraps a function as a pipeline Step.
type FuncStep struct {
	name string
	fn   func(ctx context.Context, sc *StepContext) error
}

// NewFuncStep creates a step from a function.
func NewFuncStep(name string, fn func(ctx context.Context, sc *StepContext) error) *FuncStep {
	return &FuncStep{name: name, fn: fn}
}

// Name returns the step name.
func (s *FuncStep) Name() string { return s.name }

// Run executes the function.
func (s *FuncStep) Run(ctx context.Context, sc *StepContext) error {
	return s.fn(ctx, sc)
}
