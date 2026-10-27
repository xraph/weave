package engine_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	gometrics "github.com/xraph/go-utils/metrics"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/vectorstore"
)

const secret = "alice@example.com"

// recordSpans installs a span recorder as the global tracer provider for the
// test and puts the old provider back afterwards.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	prev := otel.GetTracerProvider()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	return rec
}

// fakeSink is a go-utils metrics sink that keeps one handle per name and
// label set, so a test can read what was recorded where.
type fakeSink struct {
	*gometrics.MockMetrics
	mu     sync.Mutex
	series map[string]*fakeSeries
}

type fakeSeries struct {
	name   string
	labels map[string]string
	hist   *gometrics.MockHistogram
	ctr    *gometrics.MockCounter
}

func seriesKey(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		fmt.Fprintf(&b, ",%s=%s", k, labels[k])
	}
	return b.String()
}

func newFakeSink() *fakeSink {
	s := &fakeSink{MockMetrics: &gometrics.MockMetrics{}, series: map[string]*fakeSeries{}}
	get := func(name string, opts []gometrics.MetricOption) *fakeSeries {
		o := &gometrics.MetricOptions{}
		for _, opt := range opts {
			opt(o)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		key := seriesKey(name, o.Labels)
		fs, ok := s.series[key]
		if !ok {
			fs = &fakeSeries{name: name, labels: o.Labels, hist: gometrics.NewMockHistogram(), ctr: gometrics.NewMockCounter()}
			s.series[key] = fs
		}
		return fs
	}
	s.HistogramFunc = func(name string, opts ...gometrics.MetricOption) gometrics.Histogram { return get(name, opts).hist }
	s.CounterFunc = func(name string, opts ...gometrics.MetricOption) gometrics.Counter { return get(name, opts).ctr }
	return s
}

// observations returns how many observations the histogram series with
// exactly these labels has.
func (s *fakeSink) observations(name string, labels map[string]string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fs, ok := s.series[seriesKey(name, labels)]; ok {
		return fs.hist.Count()
	}
	return 0
}

func (s *fakeSink) counter(name string, labels map[string]string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if fs, ok := s.series[seriesKey(name, labels)]; ok {
		return fs.ctr.Value()
	}
	return 0
}

// byName returns every series recorded under name.
func (s *fakeSink) byName(name string) []*fakeSeries {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*fakeSeries
	for _, fs := range s.series {
		if fs.name == name {
			out = append(out, fs)
		}
	}
	return out
}

// describedEmbedder is a hash embedder that describes itself and reports a
// token count per input, the way the OpenAI embedder does.
type describedEmbedder struct {
	*embedtest.Hash
	kind   string
	model  string
	tokens int
	err    error
}

func (d describedEmbedder) Embed(ctx context.Context, texts []string) ([]embedder.EmbedResult, error) {
	if d.err != nil {
		return nil, d.err
	}
	out, err := d.Hash.Embed(ctx, texts)
	for i := range out {
		out[i].TokenCount = d.tokens
	}
	return out, err
}

func (d describedEmbedder) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: d.kind, Params: map[string]string{"model": d.model}}
}

func spansNamed(rec *tracetest.SpanRecorder, name string) []sdktrace.ReadOnlySpan {
	var out []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out
}

func oneSpan(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	got := spansNamed(rec, name)
	if len(got) != 1 {
		t.Fatalf("want one %q span, got %d", name, len(got))
	}
	return got[0]
}

func attrsOf(s sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	m := map[attribute.Key]attribute.Value{}
	for _, kv := range s.Attributes() {
		m[kv.Key] = kv.Value
	}
	return m
}

func wantAttr(t *testing.T, s sdktrace.ReadOnlySpan, key string, want any) {
	t.Helper()
	v, ok := attrsOf(s)[attribute.Key(key)]
	if !ok {
		t.Errorf("%s: no %s attribute", s.Name(), key)
		return
	}
	if got := v.AsInterface(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: %s = %v, want %v", s.Name(), key, got, want)
	}
}

func childOf(t *testing.T, child, parent sdktrace.ReadOnlySpan) {
	t.Helper()
	if child.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Errorf("%s is not a child of %s", child.Name(), parent.Name())
	}
}

// assertNoSecret fails when secret appears in any span name, attribute,
// event or status.
func assertNoSecret(t *testing.T, rec *tracetest.SpanRecorder) {
	t.Helper()
	check := func(where, s string) {
		if strings.Contains(s, secret) {
			t.Errorf("%s carries %q", where, secret)
		}
	}
	for _, s := range rec.Ended() {
		check("span name", s.Name())
		check(s.Name()+" status", s.Status().Description)
		for _, kv := range s.Attributes() {
			check(s.Name()+" attribute "+string(kv.Key), kv.Value.String())
		}
		for _, ev := range s.Events() {
			check(s.Name()+" event", ev.Name)
			for _, kv := range ev.Attributes {
				check(s.Name()+" event attribute "+string(kv.Key), kv.Value.String())
			}
		}
	}
}

func TestIngestSpansAndMetrics(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t,
		withOpt(engine.WithMetrics(sink)),
		withOpt(engine.WithEmbedder(describedEmbedder{Hash: embedtest.New(64), kind: "openai", model: "text-embedding-3-small", tokens: 7})),
	)
	col := mustCollectionAs(t, weave.WithTenant(context.Background(), secret), r.Engine, "telemetry")

	ctx, parent := otel.Tracer("test").Start(weave.WithTenant(context.Background(), secret), "request")
	res, err := r.Engine.Ingest(ctx, &engine.IngestInput{
		CollectionID: col.ID, Title: secret, Source: secret,
		Content:  "refunds are issued within thirty days to " + secret,
		Metadata: map[string]string{"owner": secret},
	})
	parent.End()
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	ingest := oneSpan(t, rec, "ingest document")
	childOf(t, ingest, spansNamed(rec, "request")[0])
	if ingest.SpanKind() != trace.SpanKindInternal {
		t.Errorf("ingest kind = %v", ingest.SpanKind())
	}
	wantAttr(t, ingest, "weave.collection.id", col.ID.String())
	wantAttr(t, ingest, "weave.document.id", res.DocumentID.String())
	wantAttr(t, ingest, "weave.chunker.kind", "recursive")
	wantAttr(t, ingest, "weave.vectorstore.kind", "memory")
	wantAttr(t, ingest, "weave.chunk.count", res.ChunkCount)
	wantAttr(t, ingest, "weave.result", "ok")
	if ingest.Status().Code != codes.Unset {
		t.Errorf("ingest status = %v", ingest.Status())
	}

	chunkSpan := oneSpan(t, rec, "chunk document")
	childOf(t, chunkSpan, ingest)
	wantAttr(t, chunkSpan, "weave.chunk.count", res.ChunkCount)

	embed := oneSpan(t, rec, "embed texts")
	childOf(t, embed, ingest)
	wantAttr(t, embed, "gen_ai.operation.name", "embeddings")
	wantAttr(t, embed, "gen_ai.provider.name", "openai")
	wantAttr(t, embed, "gen_ai.request.model", "text-embedding-3-small")
	wantAttr(t, embed, "weave.embed.operation", "ingest")
	wantAttr(t, embed, "weave.embed.inputs", res.ChunkCount)
	wantAttr(t, embed, "gen_ai.usage.input_tokens", 7*res.ChunkCount)

	upsert := oneSpan(t, rec, "upsert vectors")
	childOf(t, upsert, ingest)
	wantAttr(t, upsert, "weave.vector.count", res.ChunkCount)
	wantAttr(t, upsert, "weave.vectorstore.kind", "memory")

	assertNoSecret(t, rec)

	if n := sink.observations("weave_ingest_duration_seconds", map[string]string{"chunker": "recursive", "result": "ok"}); n != 1 {
		t.Errorf("ingest duration observations = %d, want 1", n)
	}
	if n := sink.observations("weave_vector_operation_duration_seconds", map[string]string{"vectorstore": "memory", "operation": "upsert", "result": "ok"}); n != 1 {
		t.Errorf("upsert duration observations = %d, want 1", n)
	}
	genAI := map[string]string{
		"gen_ai.operation.name": "embeddings", "gen_ai.provider.name": "openai",
		"weave.embed.operation": "ingest", "error.type": "",
	}
	if n := sink.observations("gen_ai.client.operation.duration", genAI); n != 1 {
		t.Errorf("gen_ai duration observations = %d, want 1", n)
	}
	tokens := map[string]string{
		"gen_ai.operation.name": "embeddings", "gen_ai.provider.name": "openai",
		"weave.embed.operation": "ingest", "gen_ai.token.type": "input",
	}
	if n := sink.observations("gen_ai.client.token.usage", tokens); n != 1 {
		t.Errorf("gen_ai token observations = %d, want 1", n)
	}
	// The model is a span attribute only, never a label.
	for _, name := range []string{"gen_ai.client.operation.duration", "gen_ai.client.token.usage"} {
		for _, fs := range sink.byName(name) {
			if _, ok := fs.labels["gen_ai.request.model"]; ok {
				t.Errorf("%s carries the model as a label", name)
			}
		}
	}
}

// A component that describes itself with a kind Weave doesn't ship is
// labelled "custom", so a caller's string never becomes a label value.
func TestCustomKindsAreBounded(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t,
		withOpt(engine.WithMetrics(sink)),
		withOpt(engine.WithEmbedder(describedEmbedder{Hash: embedtest.New(64), kind: secret, model: secret})),
	)
	col := mustTestCollection(t, r.Engine, "kinds")
	mustIngest(t, weave.WithTenant(context.Background(), "t1"), r.Engine, col.ID, "t", "some words to embed")

	wantAttr(t, oneSpan(t, rec, "embed texts"), "gen_ai.provider.name", "custom")
	for _, fs := range sink.byName("gen_ai.client.operation.duration") {
		if fs.labels["gen_ai.provider.name"] != "custom" {
			t.Errorf("provider label = %q, want custom", fs.labels["gen_ai.provider.name"])
		}
	}
	// The model is operator config and may go on the span, but nowhere else.
	for _, fs := range sink.byName("gen_ai.client.operation.duration") {
		for k, v := range fs.labels {
			if strings.Contains(v, secret) {
				t.Errorf("label %s carries the model", k)
			}
		}
	}
}

// plainChunker and plainVectors hide the wrapped component's Describe, the
// way a component written outside Weave has none.
type plainChunker struct{ chunker.Chunker }

type plainVectors struct{ vectorstore.VectorStore }

// oddRetriever describes itself with a kind Weave doesn't ship.
type oddRetriever struct{ retriever.Retriever }

func (oddRetriever) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: secret} }

// Every stage falls back to "custom": a chunker and a vector store that
// don't describe themselves, and a retriever that names a kind of its own.
func TestCustomComponentsAreLabelledCustom(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t,
		withOpt(engine.WithMetrics(sink)),
		withOpt(engine.WithChunker(plainChunker{chunker.NewRecursiveChunker()})),
		func(r *testRig) engine.Option { return engine.WithVectorStore(plainVectors{r.Vectors}) },
		func(r *testRig) engine.Option {
			return engine.WithRetriever(oddRetriever{retriever.NewSimilarityRetriever(r.Vectors, r.Embed, r.Store)})
		},
	)
	ctx := weave.WithTenant(context.Background(), "t1")
	col := mustTestCollection(t, r.Engine, "custom")
	mustIngest(t, ctx, r.Engine, col.ID, "a", "refunds take thirty days")
	if _, err := r.Engine.Retrieve(ctx, "refunds", engine.WithCollection(col.ID)); err != nil {
		t.Fatal(err)
	}

	ingest := oneSpan(t, rec, "ingest document")
	wantAttr(t, ingest, "weave.chunker.kind", "custom")
	wantAttr(t, ingest, "weave.vectorstore.kind", "custom")
	wantAttr(t, oneSpan(t, rec, "chunk document"), "weave.chunker.kind", "custom")
	wantAttr(t, oneSpan(t, rec, "upsert vectors"), "weave.vectorstore.kind", "custom")
	wantAttr(t, oneSpan(t, rec, "retrieve chunks"), "weave.retriever.kind", "custom")
	assertNoSecret(t, rec)

	if n := sink.observations("weave_ingest_duration_seconds", map[string]string{"chunker": "custom", "result": "ok"}); n != 1 {
		t.Errorf("ingest observations with chunker=custom = %d, want 1", n)
	}
	if n := sink.observations("weave_vector_operation_duration_seconds", map[string]string{"vectorstore": "custom", "operation": "upsert", "result": "ok"}); n != 1 {
		t.Errorf("upsert observations with vectorstore=custom = %d, want 1", n)
	}
	if n := sink.observations("weave_retrieve_duration_seconds", map[string]string{"retriever": "custom", "result": "ok"}); n != 1 {
		t.Errorf("retrieve observations with retriever=custom = %d, want 1", n)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, fs := range sink.series {
		for k, v := range fs.labels {
			if strings.Contains(v, secret) {
				t.Errorf("%s label %s carries the retriever's own kind", fs.name, k)
			}
		}
	}
}

// countingEmbedder reports a token count but doesn't describe itself.
type countingEmbedder struct{ *embedtest.Hash }

func (c countingEmbedder) Embed(ctx context.Context, texts []string) ([]embedder.EmbedResult, error) {
	out, err := c.Hash.Embed(ctx, texts)
	for i := range out {
		out[i].TokenCount = 5
	}
	return out, err
}

// Usage comes from TokenCount alone. An embedder without Describe gets no
// model and the custom provider, but its tokens still count.
func TestUsageNeedsNoDescriber(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t,
		withOpt(engine.WithMetrics(sink)),
		withOpt(engine.WithEmbedder(countingEmbedder{embedtest.New(64)})),
	)
	col := mustTestCollection(t, r.Engine, "usage")
	res, err := r.Engine.Ingest(weave.WithTenant(context.Background(), "t1"), &engine.IngestInput{CollectionID: col.ID, Content: "a few words"})
	if err != nil {
		t.Fatal(err)
	}

	embed := oneSpan(t, rec, "embed texts")
	wantAttr(t, embed, "gen_ai.provider.name", "custom")
	wantAttr(t, embed, "gen_ai.usage.input_tokens", 5*res.ChunkCount)
	if _, ok := attrsOf(embed)["gen_ai.request.model"]; ok {
		t.Error("an embedder that doesn't describe itself got a model")
	}
	if n := sink.observations("gen_ai.client.token.usage", map[string]string{
		"gen_ai.operation.name": "embeddings", "gen_ai.provider.name": "custom",
		"weave.embed.operation": "ingest", "gen_ai.token.type": "input",
	}); n != 1 {
		t.Errorf("token observations = %d, want 1", n)
	}
}

func TestIngestFailureNamesTheTypeOnly(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t,
		withOpt(engine.WithMetrics(sink)),
		withOpt(engine.WithEmbedder(describedEmbedder{Hash: embedtest.New(64), kind: "openai", err: errors.New("provider said no to " + secret)})),
	)
	col := mustTestCollection(t, r.Engine, "fail")
	if _, err := r.Engine.Ingest(weave.WithTenant(context.Background(), "t1"), &engine.IngestInput{CollectionID: col.ID, Content: "hello there"}); err == nil {
		t.Fatal("ingest succeeded with a failing embedder")
	}

	// The document was chunked before the embedder failed, and the ingest
	// span says how many chunks it had, as chunk document does.
	chunks := attrsOf(oneSpan(t, rec, "chunk document"))["weave.chunk.count"].AsInt64()
	if chunks == 0 {
		t.Fatal("chunk document recorded no chunks")
	}
	wantAttr(t, oneSpan(t, rec, "ingest document"), "weave.chunk.count", chunks)

	for _, name := range []string{"embed texts", "ingest document"} {
		s := oneSpan(t, rec, name)
		if s.Status().Code != codes.Error || s.Status().Description != "*errors.errorString" {
			t.Errorf("%s status = %+v", name, s.Status())
		}
		wantAttr(t, s, "error.type", "*errors.errorString")
		if len(s.Events()) != 1 || s.Events()[0].Name != "exception" {
			t.Errorf("%s events = %+v", name, s.Events())
		}
		for _, ev := range s.Events() {
			for _, kv := range ev.Attributes {
				if kv.Key != "exception.type" {
					t.Errorf("%s exception event carries %s", name, kv.Key)
				}
			}
		}
	}
	assertNoSecret(t, rec)

	if n := sink.observations("weave_ingest_duration_seconds", map[string]string{"chunker": "recursive", "result": "error"}); n != 1 {
		t.Errorf("failed ingest observations = %d, want 1", n)
	}
	genAI := map[string]string{
		"gen_ai.operation.name": "embeddings", "gen_ai.provider.name": "openai",
		"weave.embed.operation": "ingest", "error.type": "*errors.errorString",
	}
	if n := sink.observations("gen_ai.client.operation.duration", genAI); n != 1 {
		t.Errorf("failed gen_ai observations = %d, want 1", n)
	}
	if len(sink.byName("gen_ai.client.token.usage")) != 0 {
		t.Error("a failed call recorded token usage")
	}
}

// A missing collection is the caller's mistake, not a failure: not_found,
// and no error status.
func TestIngestUnknownCollectionIsNotAnError(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t, withOpt(engine.WithMetrics(sink)))
	_, err := r.Engine.Ingest(weave.WithTenant(context.Background(), "t1"), &engine.IngestInput{CollectionID: id.NewCollectionID(), Content: "x"})
	if !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Fatalf("ingest: %v", err)
	}
	s := oneSpan(t, rec, "ingest document")
	if s.Status().Code != codes.Unset {
		t.Errorf("status = %+v", s.Status())
	}
	wantAttr(t, s, "weave.result", "not_found")
	if n := sink.observations("weave_ingest_duration_seconds", map[string]string{"chunker": "recursive", "result": "not_found"}); n != 1 {
		t.Errorf("not_found observations = %d, want 1", n)
	}
}

func TestRetrieveSpansAndMetrics(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t, withOpt(engine.WithMetrics(sink)))
	// The tenant rides on the context and must reach no span.
	ctx := weave.WithTenant(context.Background(), secret)
	col := mustCollectionAs(t, ctx, r.Engine, "retrieve")
	mustIngest(t, ctx, r.Engine, col.ID, "refunds", "refunds are issued within thirty days")
	gone := mustIngest(t, ctx, r.Engine, col.ID, "refund policy", "refund policy thirty days")
	// Drop one document's chunk rows and leave its vectors: an orphan.
	if err := r.Store.DeleteChunksByDocument(ctx, gone); err != nil {
		t.Fatal(err)
	}

	ctx, parent := otel.Tracer("test").Start(ctx, "request")
	hits, err := r.Engine.Retrieve(ctx, "refunds thirty days "+secret, engine.WithCollection(col.ID), engine.WithTopK(5))
	parent.End()
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}

	ret := oneSpan(t, rec, "retrieve chunks")
	childOf(t, ret, spansNamed(rec, "request")[0])
	wantAttr(t, ret, "weave.retriever.kind", "none")
	wantAttr(t, ret, "weave.retrieve.top_k", 5)
	wantAttr(t, ret, "weave.collection.id", col.ID.String())
	wantAttr(t, ret, "weave.retrieve.results", len(hits))
	wantAttr(t, ret, "weave.retrieve.hydrated", 1)
	wantAttr(t, ret, "weave.retrieve.orphaned", 1)
	wantAttr(t, ret, "weave.result", "ok")

	var queryEmbed sdktrace.ReadOnlySpan
	for _, s := range spansNamed(rec, "embed texts") {
		if attrsOf(s)["weave.embed.operation"].AsString() == "query" {
			queryEmbed = s
		}
	}
	if queryEmbed == nil {
		t.Fatal("no query embed span")
	}
	childOf(t, queryEmbed, ret)
	// The hash embedder doesn't describe itself or report tokens.
	wantAttr(t, queryEmbed, "gen_ai.provider.name", "custom")
	if _, ok := attrsOf(queryEmbed)["gen_ai.usage.input_tokens"]; ok {
		t.Error("an embedder that reports no tokens got a usage attribute")
	}

	search := oneSpan(t, rec, "search vectors")
	childOf(t, search, ret)
	wantAttr(t, search, "weave.search.top_k", 5)
	wantAttr(t, search, "weave.search.results", len(hits))
	assertNoSecret(t, rec)

	if n := sink.observations("weave_retrieve_duration_seconds", map[string]string{"retriever": "none", "result": "ok"}); n != 1 {
		t.Errorf("retrieve observations = %d, want 1", n)
	}
	if n := sink.observations("weave_vector_operation_duration_seconds", map[string]string{"vectorstore": "memory", "operation": "search", "result": "ok"}); n != 1 {
		t.Errorf("search observations = %d, want 1", n)
	}
	if v := sink.counter("weave_retrieve_hits_orphaned_total", map[string]string{}); v != 1 {
		t.Errorf("orphaned counter = %v, want 1", v)
	}
	if n := sink.observations("gen_ai.client.operation.duration", map[string]string{
		"gen_ai.operation.name": "embeddings", "gen_ai.provider.name": "custom",
		"weave.embed.operation": "query", "error.type": "",
	}); n != 1 {
		t.Errorf("query embed observations = %d, want 1", n)
	}
}

func TestRetrieveWithRetrieverLabelsItsKind(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t, withOpt(engine.WithMetrics(sink)), func(r *testRig) engine.Option {
		return engine.WithRetriever(retriever.NewMMRRetriever(r.Vectors, r.Embed, 0.7))
	})
	ctx := weave.WithTenant(context.Background(), "t1")
	col := mustTestCollection(t, r.Engine, "mmr")
	mustIngest(t, ctx, r.Engine, col.ID, "a", "refunds are issued within thirty days")
	if _, err := r.Engine.Retrieve(ctx, "refunds", engine.WithCollection(col.ID)); err != nil {
		t.Fatal(err)
	}
	wantAttr(t, oneSpan(t, rec, "retrieve chunks"), "weave.retriever.kind", "mmr")
	if n := sink.observations("weave_retrieve_duration_seconds", map[string]string{"retriever": "mmr", "result": "ok"}); n != 1 {
		t.Errorf("retrieve observations = %d, want 1", n)
	}
}

func TestReindexSpans(t *testing.T) {
	rec := recordSpans(t)
	sink := newFakeSink()
	r := newRig(t, withOpt(engine.WithMetrics(sink)))
	ctx := weave.WithTenant(context.Background(), "t1")
	col := mustTestCollection(t, r.Engine, "reindex")
	mustIngest(t, ctx, r.Engine, col.ID, "a", "first document")
	mustIngest(t, ctx, r.Engine, col.ID, "b", "second document")

	if err := r.Engine.ReindexCollection(ctx, col.ID); err != nil {
		t.Fatal(err)
	}
	reindex := oneSpan(t, rec, "reindex collection")
	if reindex.Parent().IsValid() {
		t.Error("reindex span has a parent though the caller had no span")
	}
	wantAttr(t, reindex, "weave.collection.id", col.ID.String())
	wantAttr(t, reindex, "weave.reindex.documents", 2)
	wantAttr(t, reindex, "weave.reindex.chunks", 2)

	var n int
	for _, s := range spansNamed(rec, "embed texts") {
		if attrsOf(s)["weave.embed.operation"].AsString() == "reindex" {
			childOf(t, s, reindex)
			n++
		}
	}
	if n != 2 {
		t.Errorf("reindex embed spans = %d, want 2", n)
	}
	if got := sink.observations("gen_ai.client.operation.duration", map[string]string{
		"gen_ai.operation.name": "embeddings", "gen_ai.provider.name": "custom",
		"weave.embed.operation": "reindex", "error.type": "",
	}); got != 2 {
		t.Errorf("reindex embed observations = %d, want 2", got)
	}
}

// Without WithMetrics the engine records nothing and still runs.
func TestNoMetricsSinkIsFine(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "plain")
	mustIngest(t, weave.WithTenant(context.Background(), "t1"), r.Engine, col.ID, "a", "words")
	if _, err := r.Engine.Retrieve(weave.WithTenant(context.Background(), "t1"), "words"); err != nil {
		t.Fatal(err)
	}
}

// Every series under one name carries the same label keys.
func TestMetricLabelKeysAreStable(t *testing.T) {
	recordSpans(t)
	sink := newFakeSink()
	r := newRig(t, withOpt(engine.WithMetrics(sink)))
	ctx := weave.WithTenant(context.Background(), "t1")
	col := mustTestCollection(t, r.Engine, "keys")
	mustIngest(t, ctx, r.Engine, col.ID, "a", "words")
	_, _ = r.Engine.Ingest(ctx, &engine.IngestInput{CollectionID: id.NewCollectionID(), Content: "x"}) // a not_found series is the point
	if _, err := r.Engine.Retrieve(ctx, "words"); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"weave_ingest_duration_seconds":           "chunker,result",
		"weave_retrieve_duration_seconds":         "result,retriever",
		"weave_vector_operation_duration_seconds": "operation,result,vectorstore",
		"weave_retrieve_hits_orphaned_total":      "",
		"gen_ai.client.operation.duration":        "error.type,gen_ai.operation.name,gen_ai.provider.name,weave.embed.operation",
		"gen_ai.client.token.usage":               "gen_ai.operation.name,gen_ai.provider.name,gen_ai.token.type,weave.embed.operation",
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for _, fs := range sink.series {
		keys := make([]string, 0, len(fs.labels))
		for k := range fs.labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w, ok := want[fs.name]
		if !ok {
			t.Errorf("unexpected metric %s", fs.name)
			continue
		}
		if got := strings.Join(keys, ","); got != w {
			t.Errorf("%s label keys = %q, want %q", fs.name, got, w)
		}
	}
}
