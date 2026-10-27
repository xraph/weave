// Package telemetry holds the OpenTelemetry plumbing the engine and the
// embedders share: the tracer, the span attribute keys, how an error is
// named, the bounded component labels, and the metric families recorded on a
// go-utils metrics sink.
//
// Spans go to the global tracer provider, which forge.New installs. Without
// one they cost a no-op tracer call. Metrics are recorded only on a sink the
// caller hands over (engine.WithMetrics), never on otel.Meter: a Forge app
// installs no global MeterProvider, so nothing recorded there is exported.
package telemetry

import (
	"context"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/weave"
)

// InstrumentationName is the instrumentation scope on every span Weave starts.
const InstrumentationName = "github.com/xraph/weave"

// Tracer returns the tracer Weave starts its spans with. It reads the global
// tracer provider on every call, so whichever provider is installed when the
// span starts is the one that records it.
func Tracer() trace.Tracer { return otel.Tracer(InstrumentationName) }

// Span attribute keys. The gen_ai, http, server and error ones follow the
// OpenTelemetry semantic conventions; the weave ones are this module's own.
// Values are counts, booleans, bounded enum strings and opaque resource IDs.
// Query text, document and chunk content, titles, sources, metadata, tenant
// IDs and keys never go on a span.
const (
	GenAIOperationName    = attribute.Key("gen_ai.operation.name")
	GenAIProviderName     = attribute.Key("gen_ai.provider.name")
	GenAIRequestModel     = attribute.Key("gen_ai.request.model")
	GenAIUsageInputTokens = attribute.Key("gen_ai.usage.input_tokens")
	ErrorTypeKey          = attribute.Key("error.type")

	HTTPRequestMethod      = attribute.Key("http.request.method")
	HTTPResponseStatusCode = attribute.Key("http.response.status_code")
	ServerAddress          = attribute.Key("server.address")
	ServerPort             = attribute.Key("server.port")

	CollectionID     = attribute.Key("weave.collection.id")
	DocumentID       = attribute.Key("weave.document.id")
	Result           = attribute.Key("weave.result")
	ChunkerKind      = attribute.Key("weave.chunker.kind")
	VectorStoreKind  = attribute.Key("weave.vectorstore.kind")
	RetrieverKind    = attribute.Key("weave.retriever.kind")
	ContentBytes     = attribute.Key("weave.content.bytes")
	ChunkCount       = attribute.Key("weave.chunk.count")
	EmbedOperation   = attribute.Key("weave.embed.operation")
	EmbedInputs      = attribute.Key("weave.embed.inputs")
	VectorCount      = attribute.Key("weave.vector.count")
	SearchTopK       = attribute.Key("weave.search.top_k")
	SearchResults    = attribute.Key("weave.search.results")
	RetrieveTopK     = attribute.Key("weave.retrieve.top_k")
	RetrieveResults  = attribute.Key("weave.retrieve.results")
	RetrieveHydrated = attribute.Key("weave.retrieve.hydrated")
	RetrieveOrphaned = attribute.Key("weave.retrieve.orphaned")
	ReindexDocuments = attribute.Key("weave.reindex.documents")
	ReindexChunks    = attribute.Key("weave.reindex.chunks")
	RecoverListed    = attribute.Key("weave.recover.listed")
	RecoverFailed    = attribute.Key("weave.recover.failed")
	RecoverPurged    = attribute.Key("weave.recover.purged")
	RecoverSkipped   = attribute.Key("weave.recover.skipped_no_tenant")
	IngestUnchanged  = attribute.Key("weave.ingest.unchanged")
	IngestReplaced   = attribute.Key("weave.ingest.replaced")
)

// OperationEmbeddings is gen_ai.operation.name for an embedding call.
const OperationEmbeddings = "embeddings"

// Embed operations: what an embedding call was for. They are the values of
// weave.embed.operation on spans and metrics.
const (
	EmbedIngest  = "ingest"
	EmbedQuery   = "query"
	EmbedReindex = "reindex"
)

// Result values for spans and metric labels.
const (
	ResultOK       = "ok"
	ResultError    = "error"
	ResultNotFound = "not_found"
)

// None is the component kind for a stage that is not configured, and Custom
// the kind for a component whose kind is not one Weave ships.
const (
	None   = "none"
	Custom = "custom"
)

// Component kinds Weave ships. A component reports its kind through
// weave.Describer; any kind outside these sets is reported as "custom", so a
// metric label never carries a string a caller chose.
var (
	ChunkerKinds     = kindSet("fixed", "recursive", "semantic", "sliding", "code")
	EmbedderKinds    = kindSet("openai", "local")
	VectorStoreKinds = kindSet("memory", "pgvector", "fabriq")
	RetrieverKinds   = kindSet("similarity", "mmr", "hybrid", "rerank")
)

// KindSet is a fixed set of component kinds.
type KindSet map[string]struct{}

func kindSet(kinds ...string) KindSet {
	s := make(KindSet, len(kinds))
	for _, k := range kinds {
		s[k] = struct{}{}
	}
	return s
}

// Kind returns x's kind when it describes itself with one of the kinds in
// allowed, "custom" for any other component, and "none" for a nil x.
func Kind(x any, allowed KindSet) string {
	if x == nil {
		return None
	}
	d, ok := x.(weave.Describer)
	if !ok {
		return Custom
	}
	if k := d.Describe().Kind; k != "" {
		if _, ok := allowed[k]; ok {
			return k
		}
	}
	return Custom
}

// sentinels name Weave's own sentinel errors, which are all errors.New values
// and would otherwise all report as *errors.errorString.
var sentinels = []struct {
	err  error
	name string
}{
	{weave.ErrCollectionNotFound, "weave.ErrCollectionNotFound"},
	{weave.ErrDocumentNotFound, "weave.ErrDocumentNotFound"},
	{weave.ErrChunkNotFound, "weave.ErrChunkNotFound"},
	{weave.ErrCollectionAlreadyExists, "weave.ErrCollectionAlreadyExists"},
	{weave.ErrDocumentAlreadyExists, "weave.ErrDocumentAlreadyExists"},
	{weave.ErrDuplicateDocument, "weave.ErrDuplicateDocument"},
	{weave.ErrEmptyContent, "weave.ErrEmptyContent"},
	{weave.ErrInvalidArgument, "weave.ErrInvalidArgument"},
	{weave.ErrTenantRequired, "weave.ErrTenantRequired"},
	{weave.ErrStoreClosed, "weave.ErrStoreClosed"},
	{weave.ErrIngestInterrupted, "weave.ErrIngestInterrupted"},
	{weave.ErrInvalidState, "weave.ErrInvalidState"},
}

// ErrorType names err for error.type, the exception event and log lines: the
// context error it is, the Weave sentinel it wraps, or the Go type of the
// innermost error in its chain. It never returns the error's text, which can
// quote a URL, a tenant key, a DSN or a fragment of a document. A nil err is
// "".
func ErrorType(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "context.DeadlineExceeded"
	case errors.Is(err, context.Canceled):
		return "context.Canceled"
	}
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	for next := errors.Unwrap(err); next != nil; next = errors.Unwrap(err) {
		err = next
	}
	return fmt.Sprintf("%T", err)
}

// Fail marks span as failed: error.type, an exception event that carries only
// exception.type, and an error status whose description is that type. It
// returns the type. The error's text never reaches the span.
func Fail(span trace.Span, err error) string {
	t := ErrorType(err)
	if t == "" || !span.IsRecording() {
		return t
	}
	span.SetAttributes(ErrorTypeKey.String(t))
	span.AddEvent("exception", trace.WithAttributes(attribute.String("exception.type", t)))
	span.SetStatus(codes.Error, t)
	return t
}

// ResultOf is the result label for err: "ok" for nil, "error" otherwise.
func ResultOf(err error) string {
	if err == nil {
		return ResultOK
	}
	return ResultError
}
