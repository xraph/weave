package retriever

import (
	"context"
	"fmt"
	"math"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/vectorstore"
)

// MMRRetriever uses Maximal Marginal Relevance to re-rank candidates,
// balancing relevance and diversity.
type MMRRetriever struct {
	vs       vectorstore.VectorStore
	embedder embedder.Embedder
	lambda   float64 // Balance between relevance and diversity (0-1).
}

// NewMMRRetriever creates a new MMR retriever. Lambda controls the
// trade-off: 1.0 = pure relevance, 0.0 = pure diversity.
func NewMMRRetriever(vs vectorstore.VectorStore, emb embedder.Embedder, lambda float64) *MMRRetriever {
	if lambda <= 0 || lambda > 1 {
		lambda = 0.7
	}
	return &MMRRetriever{vs: vs, embedder: emb, lambda: lambda}
}

// Retrieve performs MMR-based retrieval.
func (r *MMRRetriever) Retrieve(ctx context.Context, query string, opts *Options) ([]Result, error) {
	embedResults, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("weave: mmr retrieve: %w", err)
	}
	if len(embedResults) == 0 {
		return nil, nil
	}

	queryVec := embedResults[0].Vector

	// Fetch more candidates than requested for re-ranking.
	candidateK := opts.TopK * 3
	if candidateK < 20 {
		candidateK = 20
	}

	searchOpts := &vectorstore.SearchOptions{
		TopK:   candidateK,
		Filter: opts.Filter,
	}
	if opts.TenantKey != "" {
		searchOpts.TenantKey = opts.TenantKey
	}
	if opts.CollectionID != "" {
		if searchOpts.Filter == nil {
			searchOpts.Filter = make(map[string]string)
		}
		searchOpts.Filter["collection_id"] = opts.CollectionID
	}

	candidates, err := r.vs.Search(ctx, queryVec, searchOpts)
	if err != nil {
		return nil, fmt.Errorf("weave: mmr retrieve: %w", err)
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	// MMR re-ranking.
	selected := make([]int, 0, opts.TopK)
	selectedVecs := make([][]float32, 0, opts.TopK)
	used := make(map[int]bool)

	for len(selected) < opts.TopK && len(selected) < len(candidates) {
		bestIdx := -1
		bestScore := math.Inf(-1)

		for i, c := range candidates {
			if used[i] {
				continue
			}

			relevance := c.Score
			maxSim := 0.0
			for _, sv := range selectedVecs {
				sim := cosine(c.Vector, sv)
				if sim > maxSim {
					maxSim = sim
				}
			}

			mmrScore := r.lambda*relevance - (1-r.lambda)*maxSim
			if mmrScore > bestScore {
				bestScore = mmrScore
				bestIdx = i
			}
		}

		if bestIdx == -1 {
			break
		}

		selected = append(selected, bestIdx)
		selectedVecs = append(selectedVecs, candidates[bestIdx].Vector)
		used[bestIdx] = true
	}

	results := make([]Result, len(selected))
	for i, idx := range selected {
		c := candidates[idx]
		results[i] = Result{
			Chunk: &chunk.Chunk{
				Content:  c.Content,
				Metadata: c.Metadata,
			},
			Score: c.Score,
		}
	}
	return results, nil
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
