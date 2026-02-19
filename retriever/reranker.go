package retriever

import (
	"context"
	"sort"
)

// Reranker scores query-document pairs for re-ordering.
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]float64, error)
}

// RerankerRetriever wraps a base retriever and re-orders results using
// a cross-encoder or other reranker.
type RerankerRetriever struct {
	base     Retriever
	reranker Reranker
}

// NewRerankerRetriever creates a retriever that re-ranks results from the base retriever.
func NewRerankerRetriever(base Retriever, reranker Reranker) *RerankerRetriever {
	return &RerankerRetriever{base: base, reranker: reranker}
}

// Retrieve fetches candidates from the base retriever and re-ranks them.
func (r *RerankerRetriever) Retrieve(ctx context.Context, query string, opts *Options) ([]Result, error) {
	// Fetch more candidates for re-ranking.
	expandedOpts := *opts
	expandedOpts.TopK = opts.TopK * 3
	if expandedOpts.TopK < 20 {
		expandedOpts.TopK = 20
	}

	candidates, err := r.base.Retrieve(ctx, query, &expandedOpts)
	if err != nil {
		return nil, err
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// Extract documents for reranking.
	docs := make([]string, len(candidates))
	for i, c := range candidates {
		docs[i] = c.Chunk.Content
	}

	scores, err := r.reranker.Rerank(ctx, query, docs)
	if err != nil {
		return nil, err
	}

	// Update scores and sort.
	for i := range candidates {
		if i < len(scores) {
			candidates[i].Score = scores[i]
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if opts.TopK > 0 && len(candidates) > opts.TopK {
		candidates = candidates[:opts.TopK]
	}

	return candidates, nil
}
