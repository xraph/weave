package retriever

import (
	"fmt"

	"github.com/xraph/weave"
)

var (
	_ weave.Describer = (*SimilarityRetriever)(nil)
	_ weave.Describer = (*MMRRetriever)(nil)
	_ weave.Describer = (*HybridRetriever)(nil)
	_ weave.Describer = (*RerankerRetriever)(nil)
)

func describeAny(x any) weave.ComponentInfo {
	if d, ok := x.(weave.Describer); ok {
		return d.Describe()
	}
	return weave.ComponentInfo{Kind: "custom", Score: weave.ScoreUnknown}
}

// Describe reports a similarity retriever. Its score is whatever the vector
// store underneath reports.
func (r *SimilarityRetriever) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "similarity", Score: describeAny(r.vs).Score}
}

// Describe reports an MMR retriever and its lambda.
func (r *MMRRetriever) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "mmr", Score: weave.ScoreMMR, Params: map[string]string{"lambda": fmt.Sprintf("%.2f", r.lambda)}}
}

// Describe reports a hybrid retriever, its RRF constant and its parts.
func (r *HybridRetriever) Describe() weave.ComponentInfo {
	children := make([]weave.ComponentInfo, len(r.retrievers))
	for i, sub := range r.retrievers {
		children[i] = describeAny(sub)
	}
	return weave.ComponentInfo{Kind: "hybrid", Score: weave.ScoreRRF, Params: map[string]string{"k": fmt.Sprintf("%g", r.k)}, Children: children}
}

// Describe reports a reranking retriever and the retriever it wraps.
func (r *RerankerRetriever) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "rerank", Score: weave.ScoreRerank, Children: []weave.ComponentInfo{describeAny(r.base)}}
}
