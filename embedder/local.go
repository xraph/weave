package embedder

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned when a feature is not yet implemented.
var ErrNotImplemented = errors.New("weave: not implemented")

// LocalEmbedder is a placeholder for local model embedding (e.g., ONNX).
type LocalEmbedder struct {
	dimensions int
}

// NewLocalEmbedder creates a placeholder local embedder.
func NewLocalEmbedder(dimensions int) *LocalEmbedder {
	return &LocalEmbedder{dimensions: dimensions}
}

// Embed is not yet implemented.
func (e *LocalEmbedder) Embed(_ context.Context, _ []string) ([]EmbedResult, error) {
	return nil, ErrNotImplemented
}

// Dimensions returns the configured dimensionality.
func (e *LocalEmbedder) Dimensions() int { return e.dimensions }
