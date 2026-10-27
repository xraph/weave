package nexus_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/baggage"

	nexusgw "github.com/xraph/nexus"
	nexuspipeline "github.com/xraph/nexus/pipeline"
	"github.com/xraph/nexus/pipeline/middlewares"
	"github.com/xraph/nexus/provider"
	"github.com/xraph/nexus/tenant"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/embedder/nexus"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/store/memory"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

const smallModel = "text-embedding-3-small"

// fakeProvider is an embeddings provider that records every request it is
// sent. For the text "i" it returns the pattern [i, i+0.5, i+0.25, -i],
// repeated to the width the request asks for (4 when it asks for none).
type fakeProvider struct {
	name string
	// shortens is what SupportsDimensions answers.
	shortens bool
	// width, when set, is returned whatever the request asks for.
	width int
	// dropLast returns one vector fewer than there are inputs.
	dropLast bool
	// tokens is the usage reported for each call.
	tokens int

	mu   sync.Mutex
	reqs []provider.EmbeddingRequest
}

func newFake(name string) *fakeProvider {
	return &fakeProvider{name: name, shortens: true}
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{Embeddings: true}
}

func (f *fakeProvider) Models(context.Context) ([]provider.Model, error) {
	caps := provider.Capabilities{Embeddings: true}
	return []provider.Model{
		{ID: smallModel, Provider: f.name, Name: smallModel, Capabilities: caps},
		{ID: "text-embedding-3-large", Provider: f.name, Name: "text-embedding-3-large", Capabilities: caps},
	}, nil
}

func (f *fakeProvider) Complete(context.Context, *provider.CompletionRequest) (*provider.CompletionResponse, error) {
	return nil, provider.ErrNotSupported
}

func (f *fakeProvider) CompleteStream(context.Context, *provider.CompletionRequest) (provider.Stream, error) {
	return nil, provider.ErrNotSupported
}

func (f *fakeProvider) Healthy(context.Context) bool { return true }

// SupportsDimensions makes the fake provider.DimensionsAware. Without it
// nexus refuses every request that sets Dimensions.
func (f *fakeProvider) SupportsDimensions(string) bool { return f.shortens }

func (f *fakeProvider) Embed(_ context.Context, req *provider.EmbeddingRequest) (*provider.EmbeddingResponse, error) {
	f.mu.Lock()
	f.reqs = append(f.reqs, *req)
	f.mu.Unlock()

	width := req.Dimensions
	if width == 0 {
		width = 4
	}
	if f.width != 0 {
		width = f.width
	}
	vecs := make([][]float64, 0, len(req.Input))
	for _, text := range req.Input {
		vecs = append(vecs, vectorFor(text, width))
	}
	if f.dropLast {
		vecs = vecs[:len(vecs)-1]
	}
	return &provider.EmbeddingResponse{
		Provider:   f.name,
		Model:      req.Model,
		Embeddings: vecs,
		Usage:      provider.Usage{PromptTokens: f.tokens, TotalTokens: f.tokens},
	}, nil
}

func (f *fakeProvider) requests() []provider.EmbeddingRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

// vectorFor is the fake's vector for text. A text that isn't a number gets
// its length as its number.
func vectorFor(text string, width int) []float64 {
	i, err := strconv.Atoi(text)
	if err != nil {
		i = len(text)
	}
	n := float64(i)
	pattern := []float64{n, n + 0.5, n + 0.25, -n}
	v := make([]float64, width)
	for j := range v {
		v[j] = pattern[j%len(pattern)]
	}
	return v
}

func float32s(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x)
	}
	return out
}

// newGateway builds and initializes a gateway over providers.
func newGateway(t *testing.T, providers ...provider.Provider) *nexusgw.Gateway {
	t.Helper()
	opts := make([]nexusgw.Option, 0, len(providers))
	for _, p := range providers {
		opts = append(opts, nexusgw.WithProvider(p))
	}
	gw := nexusgw.New(opts...)
	if err := gw.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize gateway: %v", err)
	}
	t.Cleanup(func() { _ = gw.Shutdown(context.Background()) })
	return gw
}

// newTenant registers a nexus tenant and returns its ID. The access stage
// refuses a tenant the gateway doesn't know, so a freshly generated ID is
// not enough.
func newTenant(t *testing.T, gw *nexusgw.Gateway, slug string) string {
	t.Helper()
	tn, err := gw.Tenants().Create(context.Background(), &tenant.CreateInput{Name: slug, Slug: slug})
	if err != nil {
		t.Fatalf("create tenant %q: %v", slug, err)
	}
	return tn.ID.String()
}

func static(gw *nexusgw.Gateway) func() *nexusgw.Gateway {
	return func() *nexusgw.Gateway { return gw }
}

func TestEmbedConvertsAndOrdersVectors(t *testing.T) {
	fake := newFake("embeddings")
	fake.tokens = 10
	gw := newGateway(t, fake)
	tid := newTenant(t, gw, "acme")

	emb := nexus.New(static(gw), smallModel, 4)
	got, err := emb.Embed(weave.WithTenant(context.Background(), tid), []string{"0", "1", "2"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := []embedder.EmbedResult{
		{Vector: []float32{0, 0.5, 0.25, 0}, TokenCount: 4},
		{Vector: []float32{1, 1.5, 1.25, -1}, TokenCount: 3},
		{Vector: []float32{2, 2.5, 2.25, -2}, TokenCount: 3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if !slices.Equal(got[i].Vector, want[i].Vector) || got[i].TokenCount != want[i].TokenCount {
			t.Errorf("result %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEmbedReturnsNothingForNoTexts(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)

	got, err := nexus.New(static(gw), smallModel, 4).Embed(context.Background(), nil)
	if err != nil || got != nil {
		t.Fatalf("Embed(nil) = %v, %v; want nil, nil", got, err)
	}
	if n := len(fake.requests()); n != 0 {
		t.Fatalf("fake saw %d requests, want 0", n)
	}
}

func TestEmbedRejectsInvalidConfig(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)
	ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

	for _, tc := range []struct {
		name  string
		model string
		dims  int
	}{
		{"no model", "", 4},
		{"zero dimensions", smallModel, 0},
		{"negative dimensions", smallModel, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := nexus.New(static(gw), tc.model, tc.dims).Embed(ctx, []string{"0"})
			if !errors.Is(err, weave.ErrInvalidArgument) {
				t.Fatalf("Embed: %v, want ErrInvalidArgument", err)
			}
		})
	}
	if n := len(fake.requests()); n != 0 {
		t.Fatalf("fake saw %d requests, want 0", n)
	}
}

func TestEmbedPassesTheTenant(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)
	tid := newTenant(t, gw, "acme")
	mapped := newTenant(t, gw, "mapped")

	t.Run("default passes the weave tenant through", func(t *testing.T) {
		before := len(fake.requests())
		if _, err := nexus.New(static(gw), smallModel, 4).Embed(weave.WithTenant(context.Background(), tid), []string{"1"}); err != nil {
			t.Fatalf("Embed: %v", err)
		}
		reqs := fake.requests()[before:]
		if len(reqs) != 1 || reqs[0].TenantID != tid {
			t.Fatalf("fake saw %+v, want one request for tenant %s", reqs, tid)
		}
	})

	t.Run("mapper picks the nexus tenant", func(t *testing.T) {
		var gotWeave []string
		mapper := func(_ context.Context, weaveTenant string) (string, error) {
			gotWeave = append(gotWeave, weaveTenant)
			return mapped, nil
		}
		before := len(fake.requests())
		emb := nexus.New(static(gw), smallModel, 4, nexus.WithTenantMapper(mapper))
		if _, err := emb.Embed(weave.WithTenant(context.Background(), "org_1"), []string{"1"}); err != nil {
			t.Fatalf("Embed: %v", err)
		}
		if !slices.Equal(gotWeave, []string{"org_1"}) {
			t.Fatalf("mapper got %v, want [org_1]", gotWeave)
		}
		reqs := fake.requests()[before:]
		if len(reqs) != 1 || reqs[0].TenantID != mapped {
			t.Fatalf("fake saw %+v, want one request for tenant %s", reqs, mapped)
		}
	})

	t.Run("a mapper error comes back wrapped", func(t *testing.T) {
		boom := errors.New("lookup failed")
		mapper := func(context.Context, string) (string, error) { return "", boom }
		emb := nexus.New(static(gw), smallModel, 4, nexus.WithTenantMapper(mapper))
		if _, err := emb.Embed(weave.WithTenant(context.Background(), "org_1"), []string{"1"}); !errors.Is(err, boom) {
			t.Fatalf("Embed: %v, want the mapper's error", err)
		}
	})

	t.Run("nexus refuses a tenant it cannot parse", func(t *testing.T) {
		mapper := func(context.Context, string) (string, error) { return "org_1", nil }
		emb := nexus.New(static(gw), smallModel, 4, nexus.WithTenantMapper(mapper))
		_, err := emb.Embed(weave.WithTenant(context.Background(), "org_1"), []string{"1"})
		if !errors.Is(err, middlewares.ErrInvalidIdentity) {
			t.Fatalf("Embed: %v, want ErrInvalidIdentity", err)
		}
	})
}

func TestEmbedUsesNexusContextTenant(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)
	tid := newTenant(t, gw, "acme")

	called := false
	mapper := func(context.Context, string) (string, error) {
		called = true
		return "tenant_never_used", nil
	}
	emb := nexus.New(static(gw), smallModel, 4, nexus.WithTenantMapper(mapper))
	ctx := nexuspipeline.WithTenantID(weave.WithTenant(context.Background(), "org_1"), tid)
	if _, err := emb.Embed(ctx, []string{"1"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if called {
		t.Fatal("mapper was called although the context names a nexus tenant")
	}
	reqs := fake.requests()
	if len(reqs) != 1 || reqs[0].TenantID != tid {
		t.Fatalf("fake saw %+v, want one request attributed to %s", reqs, tid)
	}
}

func TestEmbedRefusesWithoutTenant(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)
	tid := newTenant(t, gw, "acme")

	t.Run("no tenant at all", func(t *testing.T) {
		_, err := nexus.New(static(gw), smallModel, 4).Embed(context.Background(), []string{"1"})
		if !errors.Is(err, weave.ErrTenantRequired) {
			t.Fatalf("Embed: %v, want ErrTenantRequired", err)
		}
	})

	t.Run("only the nexus tenant", func(t *testing.T) {
		ctx := nexuspipeline.WithTenantID(context.Background(), tid)
		_, err := nexus.New(static(gw), smallModel, 4).Embed(ctx, []string{"1"})
		if !errors.Is(err, weave.ErrTenantRequired) {
			t.Fatalf("Embed: %v, want ErrTenantRequired", err)
		}
	})

	t.Run("mapper returns nothing", func(t *testing.T) {
		mapper := func(context.Context, string) (string, error) { return "", nil }
		emb := nexus.New(static(gw), smallModel, 4, nexus.WithTenantMapper(mapper))
		_, err := emb.Embed(weave.WithTenant(context.Background(), "org_1"), []string{"1"})
		if !errors.Is(err, weave.ErrTenantRequired) {
			t.Fatalf("Embed: %v, want ErrTenantRequired", err)
		}
	})

	if n := len(fake.requests()); n != 0 {
		t.Fatalf("fake saw %d requests, want 0", n)
	}
}

func TestEmbedResolvesGatewayLazily(t *testing.T) {
	t.Run("gw answers nil, then the gateway", func(t *testing.T) {
		fake := newFake("embeddings")
		gw := newGateway(t, fake)
		ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

		calls := 0
		var current *nexusgw.Gateway
		resolve := func() *nexusgw.Gateway {
			calls++
			return current
		}
		emb := nexus.New(resolve, smallModel, 4)
		if calls != 0 {
			t.Fatalf("New called gw %d times, want 0", calls)
		}
		if _, err := emb.Embed(ctx, []string{"1"}); !errors.Is(err, nexus.ErrGatewayUnavailable) {
			t.Fatalf("Embed before the gateway exists: %v, want ErrGatewayUnavailable", err)
		}

		current = gw
		if _, err := emb.Embed(ctx, []string{"1"}); err != nil {
			t.Fatalf("Embed once the gateway exists: %v", err)
		}
		seen := calls
		current = nil
		if _, err := emb.Embed(ctx, []string{"2"}); err != nil {
			t.Fatalf("Embed on the cached gateway: %v", err)
		}
		if calls != seen {
			t.Fatalf("gw called %d more times after it answered", calls-seen)
		}
	})

	t.Run("gateway not initialized yet", func(t *testing.T) {
		// This is what nexus's extension hands out: a gateway that exists
		// from Register and is initialized in place at Start.
		fake := newFake("embeddings")
		gw := nexusgw.New(nexusgw.WithProvider(fake))

		// The gateway has no tenant store until it is initialized, so the
		// mapper names the tenant created after that.
		var tid string
		mapper := func(context.Context, string) (string, error) { return tid, nil }
		emb := nexus.New(static(gw), smallModel, 4, nexus.WithTenantMapper(mapper))
		ctx := weave.WithTenant(context.Background(), "org_1")

		tid = "tenant_placeholder"
		_, err := emb.Embed(ctx, []string{"1"})
		if !errors.Is(err, nexus.ErrGatewayUnavailable) || !errors.Is(err, nexusgw.ErrNotInitialized) {
			t.Fatalf("Embed before Initialize: %v, want ErrGatewayUnavailable and ErrNotInitialized", err)
		}

		if err := gw.Initialize(context.Background()); err != nil {
			t.Fatalf("initialize gateway: %v", err)
		}
		t.Cleanup(func() { _ = gw.Shutdown(context.Background()) })
		tid = newTenant(t, gw, "acme")
		if _, err := emb.Embed(ctx, []string{"1"}); err != nil {
			t.Fatalf("Embed after Initialize: %v", err)
		}
	})
}

func TestEmbedRoutesToTheNamedProvider(t *testing.T) {
	chat := newFake("chat-openai")
	embeddings := newFake("embeddings")
	gw := newGateway(t, chat, embeddings)
	ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

	// Without a name nexus routes by model, and both list it.
	if _, err := nexus.New(static(gw), smallModel, 4).Embed(ctx, []string{"0"}); err != nil {
		t.Fatalf("Embed without a provider: %v", err)
	}
	if len(chat.requests()) != 1 {
		t.Fatalf("control: chat-openai saw %d requests, want 1 (the routing this option exists to avoid)", len(chat.requests()))
	}

	emb := nexus.New(static(gw), smallModel, 4, nexus.WithProvider("embeddings"), nexus.WithBatchSize(1))
	if _, err := emb.Embed(ctx, []string{"0", "1", "2"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if n := len(chat.requests()); n != 1 {
		t.Fatalf("chat-openai saw %d more requests, want none", n-1)
	}
	reqs := embeddings.requests()
	if len(reqs) != 3 {
		t.Fatalf("embeddings saw %d requests, want 3", len(reqs))
	}
	for i, r := range reqs {
		if r.Provider != "embeddings" {
			t.Errorf("request %d Provider = %q, want embeddings", i, r.Provider)
		}
	}
	d, ok := emb.(weave.Describer)
	if !ok {
		t.Fatal("embedder does not describe itself")
	}
	if got := d.Describe().Params["provider"]; got != "embeddings" {
		t.Fatalf("Describe provider = %q, want embeddings", got)
	}
}

func TestEmbedSendsConfiguredDimensions(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)
	ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

	emb := nexus.New(static(gw), "text-embedding-3-large", 1024, nexus.WithBatchSize(2))
	got, err := emb.Embed(ctx, []string{"0", "1", "2"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	reqs := fake.requests()
	if len(reqs) != 2 {
		t.Fatalf("fake saw %d requests, want 2", len(reqs))
	}
	for i, r := range reqs {
		if r.Dimensions != 1024 || r.Model != "text-embedding-3-large" {
			t.Errorf("request %d: Dimensions %d, Model %q; want 1024, text-embedding-3-large", i, r.Dimensions, r.Model)
		}
		if r.Metadata != nil {
			t.Errorf("request %d carries Metadata %v, want none", i, r.Metadata)
		}
	}
	for i, r := range got {
		if len(r.Vector) != 1024 {
			t.Errorf("result %d has %d elements, want 1024", i, len(r.Vector))
		}
	}
}

func TestEmbedRefusedWhenProviderCannotShorten(t *testing.T) {
	fake := newFake("embeddings")
	fake.shortens = false
	gw := newGateway(t, fake)
	ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

	_, err := nexus.New(static(gw), smallModel, 4).Embed(ctx, []string{"1"})
	if !errors.Is(err, middlewares.ErrDimensionsUnsupported) {
		t.Fatalf("Embed: %v, want ErrDimensionsUnsupported", err)
	}
	if !strings.HasPrefix(err.Error(), "weave: ") {
		t.Fatalf("error %q does not carry the weave prefix", err)
	}
	if n := len(fake.requests()); n != 0 {
		t.Fatalf("fake saw %d requests, want 0", n)
	}
}

func TestEmbedRejectsWrongDimensions(t *testing.T) {
	t.Run("wrong width", func(t *testing.T) {
		fake := newFake("embeddings")
		fake.width = 5
		gw := newGateway(t, fake)
		ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

		_, err := nexus.New(static(gw), smallModel, 4).Embed(ctx, []string{"1"})
		if !errors.Is(err, weave.ErrDimensionMismatch) {
			t.Fatalf("Embed: %v, want ErrDimensionMismatch", err)
		}
		if !errors.Is(err, middlewares.ErrDimensionsMismatch) {
			t.Fatalf("Embed: %v, want nexus's ErrDimensionsMismatch kept in the chain", err)
		}
		for _, s := range []string{"5", "4", smallModel} {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("error %q does not mention %q", err, s)
			}
		}
	})

	t.Run("too few vectors", func(t *testing.T) {
		fake := newFake("embeddings")
		fake.dropLast = true
		gw := newGateway(t, fake)
		ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

		got, err := nexus.New(static(gw), smallModel, 4).Embed(ctx, []string{"0", "1", "2"})
		if err == nil {
			t.Fatalf("Embed returned %d results for 3 texts and no error", len(got))
		}
		if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "3") {
			t.Errorf("error %q does not name both counts", err)
		}
	})
}

func TestEmbedBatches(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)
	ctx := weave.WithTenant(context.Background(), newTenant(t, gw, "acme"))

	texts := []string{"0", "1", "2", "3", "4"}
	got, err := nexus.New(static(gw), smallModel, 4, nexus.WithBatchSize(2)).Embed(ctx, texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	reqs := fake.requests()
	sizes := make([]int, 0, len(reqs))
	for _, r := range reqs {
		sizes = append(sizes, len(r.Input))
	}
	if !slices.Equal(sizes, []int{2, 2, 1}) {
		t.Fatalf("request sizes %v, want [2 2 1]", sizes)
	}
	if len(got) != len(texts) {
		t.Fatalf("got %d results, want %d", len(got), len(texts))
	}
	for i, text := range texts {
		if want := float32s(vectorFor(text, 4)); !slices.Equal(got[i].Vector, want) {
			t.Errorf("result %d = %v, want %v", i, got[i].Vector, want)
		}
	}
}

// baggageProbe is a pipeline stage that records the baggage on each request
// it sees. Host stages run on the context Embed hands nexus, so this is
// where baggage the embedder forgot to drop would show.
type baggageProbe struct {
	mu   sync.Mutex
	seen []int
}

func (*baggageProbe) Name() string  { return "baggage_probe" }
func (*baggageProbe) Priority() int { return 1 }

func (b *baggageProbe) Process(ctx context.Context, _ *nexuspipeline.Request, next nexuspipeline.NextFunc) (*nexuspipeline.Response, error) {
	b.mu.Lock()
	b.seen = append(b.seen, baggage.FromContext(ctx).Len())
	b.mu.Unlock()
	return next(ctx)
}

func TestEmbedDropsBaggage(t *testing.T) {
	fake := newFake("embeddings")
	probe := &baggageProbe{}
	gw := nexusgw.New(nexusgw.WithProvider(fake), nexusgw.WithMiddleware(probe))
	if err := gw.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize gateway: %v", err)
	}
	t.Cleanup(func() { _ = gw.Shutdown(context.Background()) })
	tid := newTenant(t, gw, "acme")

	member, err := baggage.NewMember("user.id", "u-1")
	if err != nil {
		t.Fatalf("baggage member: %v", err)
	}
	bag, err := baggage.New(member)
	if err != nil {
		t.Fatalf("baggage: %v", err)
	}
	ctx := baggage.ContextWithBaggage(weave.WithTenant(context.Background(), tid), bag)
	if _, err := nexus.New(static(gw), smallModel, 4).Embed(ctx, []string{"1"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if !slices.Equal(probe.seen, []int{0}) {
		t.Fatalf("nexus pipeline saw baggage of sizes %v, want [0]", probe.seen)
	}
}

func TestDescribeReportsNexus(t *testing.T) {
	emb := nexus.New(func() *nexusgw.Gateway { return nil }, smallModel, 4)
	d, ok := emb.(weave.Describer)
	if !ok {
		t.Fatal("embedder does not describe itself")
	}
	got := d.Describe()
	if got.Kind != "nexus" || len(got.Params) != 2 || got.Params["model"] != smallModel || got.Params["dimensions"] != "4" {
		t.Fatalf("Describe() = %+v, want kind nexus with model and dimensions", got)
	}
	if emb.Dimensions() != 4 {
		t.Fatalf("Dimensions() = %d, want 4", emb.Dimensions())
	}
}

func TestEngineUsesNexusEmbedder(t *testing.T) {
	fake := newFake("embeddings")
	gw := newGateway(t, fake)
	tid := newTenant(t, gw, "acme")
	ctx := weave.WithTenant(context.Background(), tid)

	eng, err := engine.New(
		engine.WithStore(memory.New()),
		engine.WithVectorStore(vsmemory.New()),
		engine.WithEmbedder(nexus.New(static(gw), smallModel, 4, nexus.WithProvider("embeddings"))),
		engine.WithChunker(chunker.NewRecursiveChunker()),
	)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	col := &collection.Collection{Name: "docs", ChunkSize: 512}
	if err = eng.CreateCollection(ctx, col); err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	res, err := eng.Ingest(ctx, &engine.IngestInput{CollectionID: col.ID, Title: "hello", Content: "hello from nexus"})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	hits, err := eng.Retrieve(ctx, "hello", engine.WithCollection(col.ID))
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk == nil || hits[0].Chunk.DocumentID != res.DocumentID {
		t.Fatalf("Retrieve = %+v, want the ingested document", hits)
	}
	if kind := eng.Components().Embedder.Kind; kind != "nexus" {
		t.Fatalf("Components().Embedder.Kind = %q, want nexus", kind)
	}
	for i, r := range fake.requests() {
		if r.TenantID != tid {
			t.Errorf("request %d attributed to %q, want %s", i, r.TenantID, tid)
		}
	}
}
