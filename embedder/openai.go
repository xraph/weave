package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// OpenAIEmbedder generates embeddings using the OpenAI Embeddings API.
type OpenAIEmbedder struct {
	apiKey     string
	model      string
	dimensions int
	baseURL    string
	client     *http.Client
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
func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(e *OpenAIEmbedder) { e.baseURL = url }
}

// WithOpenAIClient sets a custom HTTP client.
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
	return e
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("weave: openai embed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weave: openai embed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weave: openai embed: status %d", resp.StatusCode)
	}

	var openAIResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&openAIResp); err != nil {
		return nil, fmt.Errorf("weave: openai embed: %w", err)
	}

	tokensPerInput := 0
	if len(texts) > 0 {
		tokensPerInput = openAIResp.Usage.TotalTokens / len(texts)
	}

	results := make([]EmbedResult, len(openAIResp.Data))
	for _, emb := range openAIResp.Data {
		results[emb.Index] = EmbedResult{
			Vector:     emb.Embedding,
			TokenCount: tokensPerInput,
		}
	}
	return results, nil
}

// Dimensions returns the embedding dimensionality.
func (e *OpenAIEmbedder) Dimensions() int { return e.dimensions }
