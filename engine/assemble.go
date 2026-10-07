package engine

import (
	"context"
	"fmt"

	"github.com/xraph/weave/assembler"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/retriever"
)

// defaultMaxTokens matches assembler.New's own default.
const defaultMaxTokens = 4096

// AssembleParams configures Assemble.
type AssembleParams struct {
	// MaxTokens is the token budget. Zero or less uses 4096.
	MaxTokens int
}

// AssembledContext is what Weave's default assembler builds from a ranking.
// It is not necessarily what an application sends: an app may assemble its
// own way.
type AssembledContext struct {
	Context     string `json:"context"`
	TotalTokens int    `json:"total_tokens"`
	MaxTokens   int    `json:"max_tokens"`
	// Included lists the 0-based positions of the hits that made it in, in
	// order. Marker [n] in Context is hit Included[n-1].
	Included []int `json:"included"`
	// FirstExcluded is the position where the budget first ran out, or -1
	// when everything fit. Hits after it may still be included.
	FirstExcluded int `json:"first_excluded"`
	// TokenCounter names how tokens were estimated.
	TokenCounter string `json:"token_counter"`
}

// Assemble runs the default assembler over hits.
func (e *Engine) Assemble(ctx context.Context, hits []ScoredChunk, p AssembleParams) (*AssembledContext, error) {
	maxTokens := p.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	// Build a mapping from filtered position to original position, skipping nil chunks.
	// This allows us to report the exact original positions in the result.
	results := make([]retriever.Result, 0, len(hits))
	posMap := make(map[int]int) // filteredIndex -> originalIndex
	for i, h := range hits {
		if h.Chunk != nil {
			posMap[len(results)] = i
			results = append(results, retriever.Result{Chunk: h.Chunk, Score: h.Score})
		}
	}

	out, err := assembler.New(assembler.WithMaxTokens(maxTokens)).Assemble(ctx, results)
	if err != nil {
		return nil, fmt.Errorf("weave: assemble: %w", err)
	}

	ac := &AssembledContext{
		Context:       out.Context,
		TotalTokens:   out.TotalTokens,
		MaxTokens:     maxTokens,
		Included:      make([]int, 0, len(out.Citations)),
		FirstExcluded: -1,
		TokenCounter:  "chars/4",
	}

	// Convert filtered indices back to original indices for Included.
	in := make(map[int]bool, len(out.Citations))
	for _, c := range out.Citations {
		originalIdx := posMap[c.ChunkIndex]
		ac.Included = append(ac.Included, originalIdx)
		in[originalIdx] = true
	}

	// Find FirstExcluded: the first original position that was not included.
	for i := range hits {
		if !in[i] {
			ac.FirstExcluded = i
			break
		}
	}

	return ac, nil
}

// ChunkRef names a chunk and the score it had in some earlier ranking.
type ChunkRef struct {
	ChunkID id.ChunkID `json:"chunk_id"`
	Score   float64    `json:"score"`
}

// AssembleRefs reads each chunk back by ID and assembles them in the given
// order, without embedding anything. A missing chunk is an error: the ranking
// it came from is stale.
func (e *Engine) AssembleRefs(ctx context.Context, refs []ChunkRef, p AssembleParams) (*AssembledContext, []ScoredChunk, error) {
	hits := make([]ScoredChunk, len(refs))
	for i, ref := range refs {
		ch, err := e.GetChunk(ctx, ref.ChunkID)
		if err != nil {
			return nil, nil, fmt.Errorf("weave: assemble chunk %s: %w", ref.ChunkID, err)
		}
		hits[i] = ScoredChunk{Chunk: ch, Score: ref.Score, Hydrated: true}
	}
	ac, err := e.Assemble(ctx, hits, p)
	if err != nil {
		return nil, nil, err
	}
	return ac, hits, nil
}
