package telemetry

import (
	"strings"
	"sync"

	gometrics "github.com/xraph/go-utils/metrics"
)

// Metric names. Forge exports a name outside its semconv namespaces with its
// own prefix and no unit suffix, so the weave ones carry their unit in the
// name and scrape as forge_<name>. The gen_ai ones are the OpenTelemetry
// GenAI client metrics; gen_ai is not a forge semconv namespace, so they
// scrape as forge_gen_ai_client_operation_duration and
// forge_gen_ai_client_token_usage.
const (
	MetricIngestDuration   = "weave_ingest_duration_seconds"
	MetricRetrieveDuration = "weave_retrieve_duration_seconds"
	MetricVectorDuration   = "weave_vector_operation_duration_seconds"
	MetricOrphanedHits     = "weave_retrieve_hits_orphaned_total"
	MetricGenAIDuration    = "gen_ai.client.operation.duration"
	MetricGenAITokens      = "gen_ai.client.token.usage" // #nosec G101 -- a metric name, not a credential
)

// Metric label keys. Forge turns the dots into underscores on the scrape.
const (
	LabelChunker     = "chunker"
	LabelRetriever   = "retriever"
	LabelVectorStore = "vectorstore"
	LabelOperation   = "operation"
	LabelResult      = "result"

	LabelGenAIOperation = "gen_ai.operation.name"
	LabelGenAIProvider  = "gen_ai.provider.name"
	LabelEmbedOperation = "weave.embed.operation"
	LabelErrorType      = "error.type"
	LabelTokenType      = "gen_ai.token.type" // #nosec G101 -- a label key, not a credential
)

// Vector store operations, the values of the operation label.
const (
	VectorSearch = "search"
	VectorUpsert = "upsert"
)

// TokenInput is gen_ai.token.type for input tokens. Embeddings have no
// output tokens.
const TokenInput = "input"

// Histogram bounds, in the metric's unit.
var (
	// IngestBuckets span a short note on a fast local embedder to a long
	// document embedded over the network, in seconds.
	IngestBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}
	// RetrieveBuckets span an in-memory search to a reranked hosted one, in
	// seconds.
	RetrieveBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
	// VectorBuckets span an in-memory store to a remote one under load, in
	// seconds.
	VectorBuckets = []float64{0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}
	// GenAIDurationBuckets are the bounds the GenAI conventions advise for
	// gen_ai.client.operation.duration, in seconds.
	GenAIDurationBuckets = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
	// GenAITokenBuckets are the bounds the GenAI conventions advise for
	// gen_ai.client.token.usage, in tokens.
	GenAITokenBuckets = []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}
)

// family declares a metric once with its label keys and caches one handle
// per label-value tuple, so the hot path takes no registry lock. Every label
// value is a bounded enum, so the cache stays small.
type family[T any] struct {
	keys  []string
	build func(labels map[string]string) T
	cache sync.Map // joined values -> T
}

func (f *family[T]) with(values []string) (T, bool) {
	var zero T
	if len(values) != len(f.keys) {
		return zero, false
	}
	key := strings.Join(values, "\x1f")
	if v, ok := f.cache.Load(key); ok {
		if h, ok := v.(T); ok {
			return h, true
		}
	}
	labels := make(map[string]string, len(f.keys))
	for i, k := range f.keys {
		labels[k] = values[i]
	}
	v, _ := f.cache.LoadOrStore(key, f.build(labels))
	h, ok := v.(T)
	return h, ok
}

// Counter is a counter declared with label keys. A nil *Counter records
// nothing.
type Counter struct{ f *family[gometrics.Counter] }

// NewCounter declares a counter on m. A nil m returns nil. A counter with
// no labels is created right away, so it is exported as 0 before its first
// increment and a rate over it never starts from a missing series.
func NewCounter(m gometrics.Metrics, name, help string, keys ...string) *Counter {
	if m == nil {
		return nil
	}
	c := &Counter{f: &family[gometrics.Counter]{keys: keys, build: func(l map[string]string) gometrics.Counter {
		return m.Counter(name, gometrics.WithDescription(help), gometrics.WithLabels(l))
	}}}
	if len(keys) == 0 {
		c.f.with(nil)
	}
	return c
}

// Add adds n to the series for values, given in the declared key order.
func (c *Counter) Add(n float64, values ...string) {
	if c == nil || n <= 0 {
		return
	}
	if h, ok := c.f.with(values); ok {
		h.Add(n)
	}
}

// Histogram is a histogram declared with label keys. A nil *Histogram
// records nothing.
type Histogram struct{ f *family[gometrics.Histogram] }

// NewHistogram declares a histogram on m. A nil m returns nil.
func NewHistogram(m gometrics.Metrics, name, help, unit string, buckets []float64, keys ...string) *Histogram {
	if m == nil {
		return nil
	}
	return &Histogram{f: &family[gometrics.Histogram]{keys: keys, build: func(l map[string]string) gometrics.Histogram {
		return m.Histogram(name, gometrics.WithDescription(help), gometrics.WithUnit(unit),
			gometrics.WithBuckets(buckets...), gometrics.WithLabels(l))
	}}}
}

// Observe records v on the series for values, given in the declared key order.
func (h *Histogram) Observe(v float64, values ...string) {
	if h == nil {
		return
	}
	if s, ok := h.f.with(values); ok {
		s.Observe(v)
	}
}
