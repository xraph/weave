// Package embedtest holds a deterministic embedder for tests. It makes no
// network call and counts how often it was asked to embed, so a test can
// prove a refused call never reached it.
package embedtest

import (
	"context"
	"hash/fnv"
	"strings"
	"sync/atomic"

	"github.com/xraph/weave/embedder"
)

// Compile-time interface check.
var _ embedder.Embedder = (*Hash)(nil)

// Hash is a bag-of-words embedder: each word adds one to a hashed dimension.
// Texts that share words score higher, so ranking tests can reason about
// order. It is safe for concurrent use.
type Hash struct {
	dims  int
	calls atomic.Int64
}

// New returns a Hash embedder producing vectors of dims numbers.
func New(dims int) *Hash {
	return &Hash{dims: dims}
}

// Embed returns one vector per text and counts the call.
func (h *Hash) Embed(_ context.Context, texts []string) ([]embedder.EmbedResult, error) {
	h.calls.Add(1)
	out := make([]embedder.EmbedResult, len(texts))
	for i, text := range texts {
		v := make([]float32, h.dims)
		for _, w := range strings.Fields(strings.ToLower(text)) {
			f := fnv.New32a()
			_, _ = f.Write([]byte(w))     // hash.Hash never returns an error
			v[f.Sum32()%uint32(h.dims)]++ //nolint:gosec // dims is a small positive test value
		}
		out[i] = embedder.EmbedResult{Vector: v}
	}
	return out, nil
}

// Dimensions returns the vector length.
func (h *Hash) Dimensions() int { return h.dims }

// Calls returns how many times Embed has run.
func (h *Hash) Calls() int { return int(h.calls.Load()) }
