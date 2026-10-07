package weave

// ScoreKind names what a retrieval score means, so a page never shows an
// RRF sum or a rerank score as though it were cosine similarity.
type ScoreKind string

const (
	// ScoreCosine is cosine similarity between query and chunk vectors.
	ScoreCosine ScoreKind = "cosine"
	// ScoreVectorSimilarity is whatever similarity the vector store reports,
	// when the store does not say which metric it uses.
	ScoreVectorSimilarity ScoreKind = "vector_similarity"
	// ScoreMMR is the vector relevance score, with results in MMR order.
	ScoreMMR ScoreKind = "mmr_relevance"
	// ScoreRRF is a reciprocal rank fusion sum. It is not comparable to cosine.
	ScoreRRF ScoreKind = "rrf"
	// ScoreRerank is a reranker's score. The vector score is not kept.
	ScoreRerank ScoreKind = "rerank"
	// ScoreUnknown means the component does not describe its score.
	ScoreUnknown ScoreKind = "unknown"
)

// ComponentInfo describes one pipeline component for an operator.
type ComponentInfo struct {
	Kind   string            `json:"kind"`
	Params map[string]string `json:"params,omitempty"`
	// Score is set by retrievers and vector stores.
	Score ScoreKind `json:"score,omitempty"`
	// TenantFilter is set by vector stores: "verified" when an exact
	// tenant_id metadata filter is known to work, otherwise "unverified".
	TenantFilter string `json:"tenant_filter,omitempty"`
	// Children are wrapped components, such as a hybrid retriever's parts.
	Children []ComponentInfo `json:"children,omitempty"`
}

// Describer is implemented by components that can describe themselves.
type Describer interface {
	Describe() ComponentInfo
}
