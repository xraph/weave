package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/xraph/weave/internal/telemetry"
)

// OpenAIEmbedder generates embeddings using the OpenAI Embeddings API.
type OpenAIEmbedder struct {
	apiKey     string
	model      string
	dimensions int
	baseURL    string
	client     *http.Client

	// serverAddress and serverPort are the host and port of baseURL, the
	// only parts of it a span records. The userinfo, path and query never
	// reach a span.
	serverAddress string
	serverPort    int
}

// StatusError is returned when the embeddings endpoint answers with a status
// other than 200 OK. Telemetry names a failed call by this type and the
// status code, never by the response body.
type StatusError struct {
	// StatusCode is the HTTP status the endpoint returned.
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("weave: openai embed: status %d", e.StatusCode)
}

// OpenAIOption configures the OpenAI embedder.
type OpenAIOption func(*OpenAIEmbedder)

// WithOpenAIModel sets the model name (default: text-embedding-3-small).
func WithOpenAIModel(model string) OpenAIOption {
	return func(e *OpenAIEmbedder) { e.model = model }
}

// WithOpenAIDimensions sets the output dimensions.
func WithOpenAIDimensions(dims int) OpenAIOption {
	return func(e *OpenAIEmbedder) { e.dimensions = dims }
}

// WithOpenAIBaseURL overrides the base URL for API-compatible endpoints.
func WithOpenAIBaseURL(baseURL string) OpenAIOption {
	return func(e *OpenAIEmbedder) { e.baseURL = baseURL }
}

// WithOpenAIClient sets a custom HTTP client. The embedder traces each call
// itself, so a client whose transport is already traced (forge.HTTPClient,
// otelhttp) records the call twice.
func WithOpenAIClient(c *http.Client) OpenAIOption {
	return func(e *OpenAIEmbedder) { e.client = c }
}

// NewOpenAIEmbedder creates an OpenAI embedder.
func NewOpenAIEmbedder(apiKey string, opts ...OpenAIOption) *OpenAIEmbedder {
	e := &OpenAIEmbedder{
		apiKey:     apiKey,
		model:      "text-embedding-3-small",
		dimensions: 1536,
		baseURL:    "https://api.openai.com",
		client:     http.DefaultClient,
	}
	for _, opt := range opts {
		opt(e)
	}
	e.serverAddress, e.serverPort = serverOf(e.baseURL)
	return e
}

// serverOf returns the host and port of a base URL for server.address and
// server.port. Hostname drops any userinfo. A URL that doesn't parse gives
// no address, and the span goes without one.
func serverOf(base string) (host string, port int) {
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" {
		return "", 0
	}
	host = u.Hostname()
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
		return host, port
	}
	switch u.Scheme {
	case "https":
		port = 443
	case "http":
		port = 80
	}
	return host, port
}

type openAIRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type openAIResponse struct {
	Data  []openAIEmbedding `json:"data"`
	Usage openAIUsage       `json:"usage"`
}

type openAIEmbedding struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type openAIUsage struct {
	TotalTokens int `json:"total_tokens"`
}

// Embed generates embeddings for the given texts.
func (e *OpenAIEmbedder) Embed(ctx context.Context, texts []string) ([]EmbedResult, error) {
	reqBody := openAIRequest{
		Input: texts,
		Model: e.model,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("weave: openai embed: %w", err)
	}

	ctx, span := e.startSpan(ctx)
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		telemetry.Fail(span, err)
		return nil, fmt.Errorf("weave: openai embed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	// The trace context goes so a provider that traces can join the trace.
	// ctx carries no baggage (see startSpan), so none is injected.
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := e.client.Do(req)
	if err != nil {
		telemetry.Fail(span, err)
		return nil, fmt.Errorf("weave: openai embed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	span.SetAttributes(telemetry.HTTPResponseStatusCode.Int(resp.StatusCode))
	if resp.StatusCode != http.StatusOK {
		// The HTTP conventions name a failed status by its code.
		code := strconv.Itoa(resp.StatusCode)
		span.SetAttributes(telemetry.ErrorTypeKey.String(code))
		span.SetStatus(codes.Error, code)
		return nil, &StatusError{StatusCode: resp.StatusCode}
	}

	var openAIResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		telemetry.Fail(span, err)
		return nil, fmt.Errorf("weave: openai embed: %w", err)
	}

	// The API reports usage for the whole batch. Each input gets an equal
	// share and the first total%n inputs one more, so the counts add up to
	// what the API reported.
	share, extra := 0, 0
	if n := len(texts); n > 0 {
		share, extra = openAIResp.Usage.TotalTokens/n, openAIResp.Usage.TotalTokens%n
	}

	results := make([]EmbedResult, len(openAIResp.Data))
	for _, emb := range openAIResp.Data {
		tokens := share
		if emb.Index < extra {
			tokens++
		}
		results[emb.Index] = EmbedResult{
			Vector:     emb.Embedding,
			TokenCount: tokens,
		}
	}
	return results, nil
}

// startSpan starts the client span for one call to the embeddings endpoint.
// It records the method, the server's host and port and, once known, the
// status. The URL's path and query, any userinfo in the base URL, the key and
// the texts never reach it.
//
// The span starts on a context without baggage, and the request is sent with
// that context. Baggage arrives from the client (forge's server middleware
// extracts it) and can hold user or tenant IDs; it must not travel to a third
// party.
func (e *OpenAIEmbedder) startSpan(ctx context.Context) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{telemetry.HTTPRequestMethod.String(http.MethodPost)}
	if e.serverAddress != "" {
		attrs = append(attrs, telemetry.ServerAddress.String(e.serverAddress))
		if e.serverPort != 0 {
			attrs = append(attrs, telemetry.ServerPort.Int(e.serverPort))
		}
	}
	return telemetry.Tracer().Start(baggage.ContextWithoutBaggage(ctx), http.MethodPost,
		trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
}

// Dimensions returns the embedding dimensionality.
func (e *OpenAIEmbedder) Dimensions() int { return e.dimensions }
