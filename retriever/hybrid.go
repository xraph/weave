package retriever

import (
	"context"
	"sort"
)

// HybridRetriever combines multiple retrievers using Reciprocal Rank Fusion (RRF).
type HybridRetriever struct {
	retrievers []Retriever
	k          float64 // RRF constant (default: 60).
}

// NewHybridRetriever creates a hybrid retriever from multiple sub-retrievers.
func NewHybridRetriever(retrievers ...Retriever) *HybridRetriever {
	return &HybridRetriever{retrievers: retrievers, k: 60}
}

// Retrieve runs all sub-retrievers and fuses results via RRF.
func (r *HybridRetriever) Retrieve(ctx context.Context, query string, opts *Options) ([]Result, error) {
	// Collect results from all retrievers.
	scores := make(map[string]float64)
	resultMap := make(map[string]Result)

	for _, ret := range r.retrievers {
		results, err := ret.Retrieve(ctx, query, opts)
		if err != nil {
			return nil, err
		}

		for rank, res := range results {
			key := res.Chunk.Content // Use content as dedup key.
			rrfScore := 1.0 / (r.k + float64(rank+1))
			scores[key] += rrfScore
			if _, exists := resultMap[key]; !exists {
				resultMap[key] = res
			}
		}
	}

	// Build fused results.
	var fused []Result
	for key, score := range scores {
		res := resultMap[key]
		res.Score = score
		fused = append(fused, res)
	}

	sort.Slice(fused, func(i, j int) bool {
		return fused[i].Score > fused[j].Score
	})

	if opts != nil && opts.TopK > 0 && len(fused) > opts.TopK {
		fused = fused[:opts.TopK]
	}

	return fused, nil
}
