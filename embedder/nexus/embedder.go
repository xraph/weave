package nexus

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"go.opentelemetry.io/otel/baggage"

	nexusgw "github.com/xraph/nexus"
	nexuspipeline "github.com/xraph/nexus/pipeline"
	"github.com/xraph/nexus/pipeline/middlewares"
	"github.com/xraph/nexus/provider"

	"github.com/xraph/weave"
	"github.com/xraph/weave/embedder"
)

// ErrGatewayUnavailable is returned while gw() still answers nil, and while
// the gateway it answered is not initialized yet (wrapping
// nexus.ErrNotInitialized). Both clear up once nexus has started, so a host
// can retry.
var ErrGatewayUnavailable = errors.New("weave: nexus gateway not available")

// defaultBatchSize is how many texts go in one nexus request unless
// WithBatchSize says otherwise.
const defaultBatchSize = 256

var (
	_ embedder.Embedder = (*Embedder)(nil)
	_ weave.Describer   = (*Embedder)(nil)
)

// Option configures the nexus embedder.
type Option func(*Embedder)

// WithTenantMapper turns the weave tenant into the nexus tenant ID the
// request is attributed to. The default passes it through unchanged, which
// suits hosts whose weave tenants are nexus tenant IDs. A mapper that
// returns "" refuses the call with weave.ErrTenantRequired, and its error
// comes back wrapped. A nil fn keeps the default.
func WithTenantMapper(fn func(ctx context.Context, weaveTenant string) (string, error)) Option {
	return func(e *Embedder) {
		if fn != nil {
			e.mapper = fn
		}
	}
}

// WithBatchSize caps the texts per nexus request (default 256). A value
// below 1 keeps the default.
func WithBatchSize(n int) Option {
	return func(e *Embedder) {
		if n > 0 {
			e.batch = n
		}
	}
}

// WithProvider names the nexus provider every request goes to, by its
// registered name (EmbeddingRequest.Provider). Without it nexus routes by
// model, which picks the wrong provider and key when a chat provider also
// lists the embedding model.
func WithProvider(name string) Option {
	return func(e *Embedder) { e.provider = name }
}

// Embedder embeds texts through a nexus gateway. Build it with New.
type Embedder struct {
	gw       func() *nexusgw.Gateway
	mu       sync.Mutex
	cached   *nexusgw.Gateway
	model    string
	dims     int
	provider string
	mapper   func(ctx context.Context, weaveTenant string) (string, error)
	batch    int
}

// New returns an embedder that calls gw's engine with model and asks for
// vectors of exactly dimensions numbers. It does not call gw. The first
// Embed resolves it and caches the first non-nil gateway, so a host can pass
// nexus's extension method value (ext.Gateway) before nexus has started.
func New(gw func() *nexusgw.Gateway, model string, dimensions int, opts ...Option) embedder.Embedder {
	e := &Embedder{
		gw:     gw,
		model:  model,
		dims:   dimensions,
		mapper: passThrough,
		batch:  defaultBatchSize,
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

func passThrough(_ context.Context, weaveTenant string) (string, error) { return weaveTenant, nil }

// Embed returns one vector per text, in input order, sending at most the
// batch size per nexus request. It needs a weave tenant on ctx; see the
// package documentation for who the spend is charged to.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([]embedder.EmbedResult, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if e.model == "" || e.dims <= 0 {
		return nil, fmt.Errorf("weave: nexus embed: model %q with %d dimensions: %w", e.model, e.dims, weave.ErrInvalidArgument)
	}
	nexusTenant, err := e.attribution(ctx)
	if err != nil {
		return nil, err
	}
	gw, err := e.gateway()
	if err != nil {
		return nil, err
	}

	// The provider nexus routes to makes the outbound call, and baggage can
	// carry user or tenant IDs. Context values, the nexus tenant among them,
	// still go through.
	ctx = baggage.ContextWithoutBaggage(ctx)
	results := make([]embedder.EmbedResult, 0, len(texts))
	for start := 0; start < len(texts); start += e.batch {
		batch := texts[start:min(start+e.batch, len(texts))]
		got, err := e.embedBatch(ctx, gw, nexusTenant, batch)
		if err != nil {
			return nil, err
		}
		results = append(results, got...)
	}
	return results, nil
}

// attribution returns the nexus tenant to put on the request. It is empty
// when the context already names one, so nexus's identity stage takes that.
// A weave tenant is required either way.
func (e *Embedder) attribution(ctx context.Context) (string, error) {
	weaveTenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return "", fmt.Errorf("weave: nexus embed: %w", err)
	}
	if nexuspipeline.TenantID(ctx) != "" {
		return "", nil
	}
	nexusTenant, err := e.mapper(ctx, weaveTenant)
	if err != nil {
		return "", fmt.Errorf("weave: nexus embed: map tenant: %w", err)
	}
	if nexusTenant == "" {
		return "", fmt.Errorf("weave: nexus embed: tenant mapper returned no nexus tenant: %w", weave.ErrTenantRequired)
	}
	return nexusTenant, nil
}

// gateway returns the cached gateway, or asks gw for one and caches it.
func (e *Embedder) gateway() (*nexusgw.Gateway, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cached != nil {
		return e.cached, nil
	}
	if e.gw != nil {
		if gw := e.gw(); gw != nil {
			e.cached = gw
			return gw, nil
		}
	}
	return nil, ErrGatewayUnavailable
}

func (e *Embedder) embedBatch(ctx context.Context, gw *nexusgw.Gateway, nexusTenant string, texts []string) ([]embedder.EmbedResult, error) {
	resp, err := gw.Engine().Embed(ctx, &provider.EmbeddingRequest{
		Model:      e.model,
		Provider:   e.provider,
		Input:      texts,
		Dimensions: e.dims,
		TenantID:   nexusTenant,
	})
	switch {
	case errors.Is(err, nexusgw.ErrNotInitialized):
		return nil, fmt.Errorf("%w: %w", ErrGatewayUnavailable, err)
	case errors.Is(err, middlewares.ErrDimensionsMismatch):
		// nexus checks the width before weave sees the response. Its text
		// names both widths.
		return nil, fmt.Errorf("%w: model %q: %w", weave.ErrDimensionMismatch, e.model, err)
	case err != nil:
		return nil, fmt.Errorf("weave: nexus embed: %w", err)
	}

	// Checked here too, whatever the provider claims: nexus doesn't count
	// the vectors.
	var vectors [][]float64
	if resp != nil {
		vectors = resp.Embeddings
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("weave: nexus embed: model %q returned %d vectors for %d inputs", e.model, len(vectors), len(texts))
	}

	// Usage covers the whole batch. Each input gets an equal share and the
	// first total%n one more, so the counts add up to what nexus reported.
	// This is the split embedder/openai.go uses; it's copied, not shared,
	// so the root module doesn't grow API for one sub-module.
	share, extra := resp.Usage.TotalTokens/len(texts), resp.Usage.TotalTokens%len(texts)

	results := make([]embedder.EmbedResult, len(vectors))
	for i, v := range vectors {
		if len(v) != e.dims {
			return nil, fmt.Errorf("%w: model %q returned %d dimensions, want %d", weave.ErrDimensionMismatch, e.model, len(v), e.dims)
		}
		vec := make([]float32, len(v))
		for j, x := range v {
			vec[j] = float32(x)
		}
		tokens := share
		if i < extra {
			tokens++
		}
		results[i] = embedder.EmbedResult{Vector: vec, TokenCount: tokens}
	}
	return results, nil
}

// Dimensions returns the configured vector size.
func (e *Embedder) Dimensions() int { return e.dims }

// Describe reports the nexus embedder, its model and dimensions, and the
// provider when WithProvider named one.
func (e *Embedder) Describe() weave.ComponentInfo {
	params := map[string]string{"model": e.model, "dimensions": strconv.Itoa(e.dims)}
	if e.provider != "" {
		params["provider"] = e.provider
	}
	return weave.ComponentInfo{Kind: "nexus", Params: params}
}
