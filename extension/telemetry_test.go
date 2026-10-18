package extension

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/xraph/forge"
	log "github.com/xraph/go-utils/log"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/vectorstore"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

// fixedEmbedder describes itself as the OpenAI embedder and reports two
// tokens per input, without a network call.
type fixedEmbedder struct{}

func (fixedEmbedder) Embed(_ context.Context, texts []string) ([]embedder.EmbedResult, error) {
	out := make([]embedder.EmbedResult, len(texts))
	for i := range out {
		out[i] = embedder.EmbedResult{Vector: []float32{1, 0, 0}, TokenCount: 2}
	}
	return out, nil
}

func (fixedEmbedder) Dimensions() int { return 3 }

func (fixedEmbedder) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "openai", Params: map[string]string{"model": "text-embedding-3-small"}}
}

// brokenDeletes is a vector store whose deletes fail with an error that
// quotes a DSN, as a driver error can.
type brokenDeletes struct{ *vsmemory.Store }

func (brokenDeletes) DeleteByMetadata(context.Context, map[string]string) error {
	return errors.New("dial postgres://weave:s3cret@db.internal:5432/weave: refused")
}

var _ vectorstore.VectorStore = brokenDeletes{}

func scrape(t *testing.T, app forge.App) string {
	t.Helper()
	p, ok := app.Metrics().(interface{ PrometheusHandler() http.Handler })
	if !ok {
		t.Fatalf("app metrics %T have no Prometheus handler", app.Metrics())
	}
	rr := httptest.NewRecorder()
	p.PrometheusHandler().ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/_/metrics", nil))
	body, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func lines(body, prefix string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix+"{") || line == prefix || strings.HasPrefix(line, prefix+" ") {
			out = append(out, line)
		}
	}
	return out
}

func hasSeries(body, prefix string, labels ...string) bool {
	for _, line := range lines(body, prefix) {
		all := true
		for _, l := range labels {
			if !strings.Contains(line, l) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

func TestExtensionWiresTelemetryIntoForge(t *testing.T) {
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	logger := log.NewTestLogger()
	app := forge.New(forge.WithAppName("weave-telemetry-test"), forge.WithAppLogger(logger))
	rec := tracetest.NewSpanRecorder()
	if err := forge.RegisterSpanProcessor(app, rec); err != nil {
		t.Fatalf("RegisterSpanProcessor: %v", err)
	}

	ext := New(
		WithStore(memory.New()),
		WithVectorStore(brokenDeletes{vsmemory.New()}),
		WithEngineOption(engine.WithEmbedder(fixedEmbedder{})),
		WithEngineOption(engine.WithChunker(chunker.NewRecursiveChunker())),
		WithDisableRoutes(),
	)
	if err := ext.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}
	eng := ext.Engine()

	ctx, span := forge.StartSpan(t.Context(), "request")
	col := &collection.Collection{Name: "docs", ChunkSize: 512}
	if err := eng.CreateCollection(ctx, col); err != nil {
		t.Fatal(err)
	}
	res, err := eng.Ingest(ctx, &engine.IngestInput{CollectionID: col.ID, Content: "refunds take thirty days"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Retrieve(ctx, "refunds", engine.WithCollection(col.ID)); err != nil {
		t.Fatal(err)
	}
	if err := eng.DeleteDocument(ctx, res.DocumentID); err != nil {
		t.Fatal(err)
	}
	span.End()

	// Spans reach forge's tracer provider under the caller's span.
	var ingest bool
	for _, s := range rec.Ended() {
		if s.InstrumentationScope().Name != "github.com/xraph/weave" {
			continue
		}
		if s.Name() == "ingest document" {
			ingest = true
			if s.Parent().SpanID() != span.SpanContext().SpanID() {
				t.Error("ingest document is not a child of the request span")
			}
		}
	}
	if !ingest {
		t.Fatal("no ingest document span reached forge's provider")
	}

	// The metrics are on the app's scrape under their forge_ names.
	body := scrape(t, app)
	checks := []struct {
		prefix string
		labels []string
	}{
		{"forge_weave_ingest_duration_seconds_count", []string{`chunker="recursive"`, `result="ok"`}},
		{"forge_weave_retrieve_duration_seconds_count", []string{`retriever="none"`, `result="ok"`}},
		{"forge_weave_vector_operation_duration_seconds_count", []string{`vectorstore="memory"`, `operation="search"`, `result="ok"`}},
		{"forge_weave_vector_operation_duration_seconds_count", []string{`operation="upsert"`}},
		{"forge_gen_ai_client_operation_duration_count", []string{
			`gen_ai_operation_name="embeddings"`, `gen_ai_provider_name="openai"`,
			`weave_embed_operation="ingest"`, `error_type=""`,
		}},
		{"forge_gen_ai_client_operation_duration_count", []string{`weave_embed_operation="query"`}},
		{"forge_gen_ai_client_token_usage_count", []string{`gen_ai_token_type="input"`, `weave_embed_operation="ingest"`}},
	}
	for _, c := range checks {
		if !hasSeries(body, c.prefix, c.labels...) {
			t.Errorf("no %s series with %v in:\n%s", c.prefix, c.labels, strings.Join(lines(body, c.prefix), "\n"))
		}
	}
	// The unlabelled counter is exported before its first orphan.
	if got := lines(body, "forge_weave_retrieve_hits_orphaned_total"); len(got) != 1 || !strings.HasSuffix(got[0], " 0") {
		t.Errorf("orphaned counter on the scrape = %v, want one series at 0", got)
	}
	if strings.Contains(body, "text-embedding-3-small") {
		t.Error("the model reached the scrape")
	}

	// The app's logger reaches the engine, and the line it logs for the
	// failed vector delete names the error's type, not its text.
	tl, ok := logger.(*log.TestLogger)
	if !ok {
		t.Fatalf("logger is %T", logger)
	}
	var found bool
	for _, e := range tl.GetLogsByLevel("WARN") {
		if e.Message != "failed to delete vector entries for document" {
			continue
		}
		found = true
		if v, _ := e.Field("error_type"); v != "*errors.errorString" {
			t.Errorf("error_type = %v", v)
		}
		for _, f := range e.Fields {
			if s, ok := f.Value().(string); ok && strings.Contains(s, "s3cret") {
				t.Errorf("field %s carries the DSN", f.Key())
			}
		}
	}
	if !found {
		t.Error("the engine did not log through the app's logger")
	}
}
