package engine

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	gometrics "github.com/xraph/go-utils/metrics"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/internal/telemetry"
	"github.com/xraph/weave/vectorstore"
)

// kinds are the bounded component labels, read once when the engine is
// built. Components never change after New, so neither do these.
type kinds struct {
	chunker     string
	embedder    string // also gen_ai.provider.name
	model       string // gen_ai.request.model, spans only
	vectorStore string
	retriever   string
}

func (e *Engine) describeKinds() kinds {
	k := kinds{
		chunker:     telemetry.None,
		embedder:    telemetry.None,
		vectorStore: telemetry.None,
		retriever:   telemetry.None,
	}
	if e.chunker != nil {
		k.chunker = telemetry.Kind(e.chunker, telemetry.ChunkerKinds)
	}
	if e.embedder != nil {
		k.embedder = telemetry.Kind(e.embedder, telemetry.EmbedderKinds)
		if d, ok := e.embedder.(weave.Describer); ok {
			k.model = d.Describe().Params["model"]
		}
	}
	if e.vectorStore != nil {
		k.vectorStore = telemetry.Kind(e.vectorStore, telemetry.VectorStoreKinds)
	}
	if e.retriever != nil {
		k.retriever = telemetry.Kind(e.retriever, telemetry.RetrieverKinds)
	}
	return k
}

// engineMetrics are the engine's instruments. Every field is nil when the
// engine has no metrics sink, and a nil instrument records nothing.
type engineMetrics struct {
	ingest        *telemetry.Histogram
	retrieve      *telemetry.Histogram
	vector        *telemetry.Histogram
	orphaned      *telemetry.Counter
	genAIDuration *telemetry.Histogram
	genAITokens   *telemetry.Histogram
}

func newEngineMetrics(m gometrics.Metrics) engineMetrics {
	if m == nil {
		return engineMetrics{}
	}
	return engineMetrics{
		ingest: telemetry.NewHistogram(m, telemetry.MetricIngestDuration,
			"Duration of one document ingest: chunk, embed and store.", "s", telemetry.IngestBuckets,
			telemetry.LabelChunker, telemetry.LabelResult),
		retrieve: telemetry.NewHistogram(m, telemetry.MetricRetrieveDuration,
			"Duration of one Retrieve call, hydration included.", "s", telemetry.RetrieveBuckets,
			telemetry.LabelRetriever, telemetry.LabelResult),
		vector: telemetry.NewHistogram(m, telemetry.MetricVectorDuration,
			"Duration of a vector store call the engine makes itself.", "s", telemetry.VectorBuckets,
			telemetry.LabelVectorStore, telemetry.LabelOperation, telemetry.LabelResult),
		orphaned: telemetry.NewCounter(m, telemetry.MetricOrphanedHits,
			"Retrieve hits whose chunk row is missing from the metadata store."),
		genAIDuration: telemetry.NewHistogram(m, telemetry.MetricGenAIDuration,
			"Duration of embedding calls made through the engine's embedder.", "s", telemetry.GenAIDurationBuckets,
			telemetry.LabelGenAIOperation, telemetry.LabelGenAIProvider, telemetry.LabelEmbedOperation, telemetry.LabelErrorType),
		genAITokens: telemetry.NewHistogram(m, telemetry.MetricGenAITokens,
			"Input tokens an embedding call used, as the embedder reported them.", "{token}", telemetry.GenAITokenBuckets,
			telemetry.LabelGenAIOperation, telemetry.LabelGenAIProvider, telemetry.LabelEmbedOperation, telemetry.LabelTokenType),
	}
}

// embed runs the engine's embedder under an "embed texts" span that carries
// the GenAI attributes, and records the GenAI metrics. op says what the
// vectors are for: ingest, query or reindex. The texts themselves never
// reach the span.
func (e *Engine) embed(ctx context.Context, op string, texts []string) ([]embedder.EmbedResult, error) {
	attrs := []attribute.KeyValue{
		telemetry.GenAIOperationName.String(telemetry.OperationEmbeddings),
		telemetry.GenAIProviderName.String(e.kinds.embedder),
		telemetry.EmbedOperation.String(op),
		telemetry.EmbedInputs.Int(len(texts)),
	}
	if e.kinds.model != "" {
		attrs = append(attrs, telemetry.GenAIRequestModel.String(e.kinds.model))
	}
	ctx, span := telemetry.Tracer().Start(ctx, "embed texts", trace.WithAttributes(attrs...))
	defer span.End()

	start := time.Now()
	results, err := e.embedder.Embed(ctx, texts)
	elapsed := time.Since(start).Seconds()

	errType := ""
	if err != nil {
		errType = telemetry.Fail(span, err)
	} else if tokens := inputTokens(results); tokens > 0 {
		span.SetAttributes(telemetry.GenAIUsageInputTokens.Int(tokens))
		e.metrics.genAITokens.Observe(float64(tokens),
			telemetry.OperationEmbeddings, e.kinds.embedder, op, telemetry.TokenInput)
	}
	e.metrics.genAIDuration.Observe(elapsed, telemetry.OperationEmbeddings, e.kinds.embedder, op, errType)
	return results, err
}

// inputTokens sums what the embedder reported per input. An embedder that
// reports nothing gives 0, and no usage is recorded.
func inputTokens(results []embedder.EmbedResult) int {
	n := 0
	for _, r := range results {
		n += r.TokenCount
	}
	return n
}

// chunk runs the chunker under a "chunk document" span.
func (e *Engine) chunk(ctx context.Context, content string, opts *chunker.Options) ([]chunker.ChunkResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "chunk document", trace.WithAttributes(
		telemetry.ChunkerKind.String(e.kinds.chunker),
	))
	defer span.End()

	results, err := e.chunker.Chunk(ctx, content, opts)
	if err != nil {
		telemetry.Fail(span, err)
		return nil, err
	}
	span.SetAttributes(telemetry.ChunkCount.Int(len(results)))
	return results, nil
}

// searchVectors runs a search on the engine's vector store under a
// "search vectors" span and records its duration.
func (e *Engine) searchVectors(ctx context.Context, vector []float32, opts *vectorstore.SearchOptions) ([]vectorstore.SearchResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "search vectors", trace.WithAttributes(
		telemetry.VectorStoreKind.String(e.kinds.vectorStore),
		telemetry.SearchTopK.Int(opts.TopK),
	))
	defer span.End()

	start := time.Now()
	results, err := e.vectorStore.Search(ctx, vector, opts)
	e.metrics.vector.Observe(time.Since(start).Seconds(), e.kinds.vectorStore, telemetry.VectorSearch, telemetry.ResultOf(err))
	if err != nil {
		telemetry.Fail(span, err)
		return nil, err
	}
	span.SetAttributes(telemetry.SearchResults.Int(len(results)))
	return results, nil
}

// upsertVectors writes entries to the engine's vector store under an
// "upsert vectors" span and records its duration.
func (e *Engine) upsertVectors(ctx context.Context, entries []vectorstore.Entry) error {
	ctx, span := telemetry.Tracer().Start(ctx, "upsert vectors", trace.WithAttributes(
		telemetry.VectorStoreKind.String(e.kinds.vectorStore),
		telemetry.VectorCount.Int(len(entries)),
	))
	defer span.End()

	start := time.Now()
	err := e.vectorStore.Upsert(ctx, entries)
	e.metrics.vector.Observe(time.Since(start).Seconds(), e.kinds.vectorStore, telemetry.VectorUpsert, telemetry.ResultOf(err))
	if err != nil {
		telemetry.Fail(span, err)
	}
	return err
}
