package embedder_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/weave/embedder"
)

const (
	apiKey   = "sk-test-not-a-real-key"
	userinfo = "alice@example.com"
	password = "hunter2"
)

// traced installs a span recorder and forge's default propagators (trace
// context and baggage) for the test, and restores the old ones afterwards.
func traced(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	rec := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return rec
}

type upstream struct {
	mu      sync.Mutex
	headers http.Header
	status  int
	tokens  int
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	u.headers = r.Header.Clone()
	status, tokens := u.status, u.tokens
	u.mu.Unlock()

	var req struct {
		Input []string `json:"input"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if status != 0 && status != http.StatusOK {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + apiKey + `"}}`))
		return
	}
	type item struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	}
	resp := struct {
		Data  []item `json:"data"`
		Usage struct {
			TotalTokens int `json:"total_tokens"`
		} `json:"usage"`
	}{}
	for i := range req.Input {
		resp.Data = append(resp.Data, item{Embedding: []float32{1, 0}, Index: i})
	}
	resp.Usage.TotalTokens = tokens
	_ = json.NewEncoder(w).Encode(resp)
}

func (u *upstream) seen() http.Header {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.headers
}

// baseWithUserinfo returns the server URL with credentials in it, the way a
// self-hosted gateway behind basic auth might be configured.
func baseWithUserinfo(t *testing.T, srv *httptest.Server) (base, host string, port int) {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(userinfo, password)
	return u.String(), u.Hostname(), p
}

func callerContext(t *testing.T) (context.Context, trace.Span) {
	t.Helper()
	member, err := baggage.NewMemberRaw("user.email", userinfo)
	if err != nil {
		t.Fatal(err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatal(err)
	}
	return otel.Tracer("test").Start(baggage.ContextWithBaggage(context.Background(), bag), "embed texts")
}

func assertClean(t *testing.T, s sdktrace.ReadOnlySpan) {
	t.Helper()
	forbidden := []string{apiKey, userinfo, password, "/v1/embeddings", "hello world"}
	check := func(where, v string) {
		for _, f := range forbidden {
			if strings.Contains(v, f) {
				t.Errorf("%s carries %q", where, f)
			}
		}
	}
	check("name", s.Name())
	check("status", s.Status().Description)
	for _, kv := range s.Attributes() {
		check("attribute "+string(kv.Key), kv.Value.String())
	}
	for _, ev := range s.Events() {
		for _, kv := range ev.Attributes {
			check("event attribute "+string(kv.Key), kv.Value.String())
		}
	}
}

func attr(s sdktrace.ReadOnlySpan, key string) (attribute.Value, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func TestOpenAIClientSpan(t *testing.T) {
	rec := traced(t)
	up := &upstream{tokens: 10}
	srv := httptest.NewServer(up)
	defer srv.Close()
	base, host, port := baseWithUserinfo(t, srv)

	emb := embedder.NewOpenAIEmbedder(apiKey, embedder.WithOpenAIBaseURL(base))
	ctx, parent := callerContext(t)
	results, err := emb.Embed(ctx, []string{"hello world", "b", "c"})
	parent.End()
	if err != nil {
		t.Fatalf("embed: %v", err)
	}

	// The upstream is a third party: it gets the trace context, never the
	// caller's baggage.
	h := up.seen()
	if h.Get("Baggage") != "" {
		t.Errorf("upstream saw baggage %q", h.Get("Baggage"))
	}
	if h.Get("Traceparent") == "" {
		t.Error("upstream saw no traceparent")
	}

	var spans []sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == http.MethodPost {
			spans = append(spans, s)
		}
	}
	if len(spans) != 1 {
		t.Fatalf("want one POST span, got %d", len(spans))
	}
	s := spans[0]
	if s.SpanKind() != trace.SpanKindClient {
		t.Errorf("kind = %v, want client", s.SpanKind())
	}
	if s.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("the client span is not a child of the caller's span")
	}
	if s.InstrumentationScope().Name != "github.com/xraph/weave" {
		t.Errorf("scope = %q", s.InstrumentationScope().Name)
	}
	want := map[string]string{
		"http.request.method":       "POST",
		"server.address":            host,
		"server.port":               strconv.Itoa(port),
		"http.response.status_code": "200",
	}
	if len(s.Attributes()) != len(want) {
		t.Errorf("attributes = %v, want exactly %v", s.Attributes(), want)
	}
	for k, v := range want {
		got, ok := attr(s, k)
		if !ok || got.String() != v {
			t.Errorf("%s = %q, want %q", k, got.String(), v)
		}
	}
	assertClean(t, s)

	// The batch's usage is split so the per-input counts add up to it.
	sum := 0
	for _, r := range results {
		sum += r.TokenCount
	}
	if sum != 10 || results[0].TokenCount != 4 || results[2].TokenCount != 3 {
		t.Errorf("token counts = %+v, want 4,3,3", results)
	}
}

func TestOpenAIStatusError(t *testing.T) {
	rec := traced(t)
	up := &upstream{status: http.StatusTooManyRequests}
	srv := httptest.NewServer(up)
	defer srv.Close()
	base, _, _ := baseWithUserinfo(t, srv)

	emb := embedder.NewOpenAIEmbedder(apiKey, embedder.WithOpenAIBaseURL(base))
	ctx, parent := callerContext(t)
	_, err := emb.Embed(ctx, []string{"hello world"})
	parent.End()

	var se *embedder.StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want a StatusError with 429", err)
	}
	if err.Error() != "weave: openai embed: status 429" {
		t.Errorf("error text changed: %q", err.Error())
	}

	for _, s := range rec.Ended() {
		if s.Name() != http.MethodPost {
			continue
		}
		if s.Status().Code != codes.Error || s.Status().Description != "429" {
			t.Errorf("status = %+v", s.Status())
		}
		if v, _ := attr(s, "error.type"); v.String() != "429" {
			t.Errorf("error.type = %q", v.String())
		}
		assertClean(t, s)
		return
	}
	t.Fatal("no POST span")
}

// A transport failure is named by its type. *url.Error's text quotes the
// URL, which must not reach the span.
func TestOpenAITransportErrorNamesTheTypeOnly(t *testing.T) {
	rec := traced(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	base, _, _ := baseWithUserinfo(t, srv)
	srv.Close() // nothing listens any more

	emb := embedder.NewOpenAIEmbedder(apiKey, embedder.WithOpenAIBaseURL(base))
	if _, err := emb.Embed(context.Background(), []string{"hello world"}); err == nil {
		t.Fatal("embed succeeded against a closed server")
	}
	for _, s := range rec.Ended() {
		if s.Name() != http.MethodPost {
			continue
		}
		if s.Status().Code != codes.Error {
			t.Errorf("status = %+v", s.Status())
		}
		assertClean(t, s)
		return
	}
	t.Fatal("no POST span")
}

// recordingTransport passes requests through and remembers what it saw.
type recordingTransport struct {
	mu   sync.Mutex
	seen []http.Header
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	rt.seen = append(rt.seen, r.Header.Clone())
	rt.mu.Unlock()
	return http.DefaultTransport.RoundTrip(r)
}

// The embedder traces at the call site, so a client you hand it keeps its
// own transport, and the default client is left exactly as it was.
func TestOpenAIClientTransportIsUntouched(t *testing.T) {
	rec := traced(t)
	srv := httptest.NewServer(&upstream{tokens: 1})
	defer srv.Close()

	rt := &recordingTransport{}
	client := &http.Client{Transport: rt}
	emb := embedder.NewOpenAIEmbedder(apiKey, embedder.WithOpenAIBaseURL(srv.URL), embedder.WithOpenAIClient(client))
	ctx, parent := callerContext(t)
	if _, err := emb.Embed(ctx, []string{"hello world"}); err != nil {
		t.Fatal(err)
	}
	parent.End()

	if client.Transport != rt {
		t.Errorf("the client's transport was replaced with %T", client.Transport)
	}
	rt.mu.Lock()
	seen := rt.seen
	rt.mu.Unlock()
	if len(seen) != 1 {
		t.Fatalf("the client's transport saw %d requests, want 1", len(seen))
	}
	if seen[0].Get("Baggage") != "" || seen[0].Get("Traceparent") == "" {
		t.Errorf("headers through a custom client: baggage %q, traceparent %q", seen[0].Get("Baggage"), seen[0].Get("Traceparent"))
	}
	var posts int
	for _, s := range rec.Ended() {
		if s.Name() == http.MethodPost {
			posts++
		}
	}
	if posts != 1 {
		t.Errorf("POST spans = %d, want 1", posts)
	}

	// The default client is shared with the whole process; the embedder
	// must not have wrapped or replaced its transport.
	def := embedder.NewOpenAIEmbedder(apiKey, embedder.WithOpenAIBaseURL(srv.URL))
	if _, err := def.Embed(context.Background(), []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if http.DefaultClient.Transport != nil {
		t.Errorf("http.DefaultClient.Transport = %T, want nil", http.DefaultClient.Transport)
	}
}
