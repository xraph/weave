package retriever

import (
	"context"
	"fmt"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/vectorstore"
)

// SimilarityRetriever wraps a VectorStore.Search for simple cosine
// similarity retrieval.
type SimilarityRetriever struct {
	vs       vectorstore.VectorStore
	embedder embedder.Embedder
	store    chunk.Store
}

// NewSimilarityRetriever creates a new similarity-based retriever.
func NewSimilarityRetriever(vs vectorstore.VectorStore, emb embedder.Embedder, store chunk.Store) *SimilarityRetriever {
	return &SimilarityRetriever{vs: vs, embedder: emb, store: store}
}

// Retrieve embeds the query and searches the vector store.
func (r *SimilarityRetriever) Retrieve(ctx context.Context, query string, opts *Options) ([]Result, error) {
	embedResults, err := r.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("weave: similarity retrieve: %w", err)
	}
	if len(embedResults) == 0 {
		return nil, nil
	}

	searchOpts := &vectorstore.SearchOptions{
		TopK:   opts.TopK,
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
	if opts.MinScore > 0 {
		searchOpts.MinScore = opts.MinScore
	}

	searchResults, err := r.vs.Search(ctx, embedResults[0].Vector, searchOpts)
	if err != nil {
		return nil, fmt.Errorf("weave: similarity retrieve: %w", err)
	}

	results := make([]Result, len(searchResults))
	for i, sr := range searchResults {
		results[i] = Result{
			Chunk: &chunk.Chunk{
				Content:  sr.Content,
				Metadata: sr.Metadata,
			},
			Score: sr.Score,
		}
	}
	return results, nil
}
