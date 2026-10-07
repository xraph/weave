package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/vectorstore"
)

// CompareParams configures RetrieveCompare.
type CompareParams struct {
	// CollectionID restricts both searches. Zero means every collection.
	CollectionID id.CollectionID
	// Tenant restricts both searches exactly; nil means every tenant.
	Tenant *string
	// TopK is how many final hits to return. Zero or less uses the config
	// default.
	TopK int
	// MinScore applies to the final hits only. The raw search ignores it, so
	// the result can say what it removed.
	MinScore float64
}

// CompareHit is a hit with its place in both rankings. VectorRank is 1-based;
// 0 means the chunk was not in the raw window at all.
type CompareHit struct {
	ScoredChunk
	Rank        int     `json:"rank"`
	VectorRank  int     `json:"vector_rank"`
	VectorScore float64 `json:"vector_score"`
}

// CompareResult is the configured ranking beside the raw vector ranking.
type CompareResult struct {
	Hits []CompareHit `json:"hits"`
	// LeftOut are raw-window hits the configured retriever did not return, in
	// vector order, at most TopK of them. Empty when nothing reorders.
	LeftOut []CompareHit `json:"left_out"`
	// Window is how many raw hits were asked for.
	Window int `json:"window"`
	// VectorMatches is how many raw hits came back, before MinScore.
	VectorMatches int `json:"vector_matches"`
	// BestVectorScore is the top raw score, or 0 with no matches.
	BestVectorScore float64 `json:"best_vector_score"`
	// Reordered is false when the final ranking is the raw ranking: no
	// retriever, or a plain similarity retriever.
	Reordered bool `json:"reordered"`
	// Score is what Hits[i].Score means.
	Score           weave.ScoreKind `json:"score"`
	RetrieverMillis float64         `json:"retriever_ms"`
	VectorMillis    float64         `json:"vector_ms"`
}

// RetrieveCompare runs the configured retriever and a raw vector search over
// a wider window, and reports where each final hit sat in the raw ranking.
// With no retriever configured it embeds the query once and derives both
// sides from the same search.
func (e *Engine) RetrieveCompare(ctx context.Context, query string, p CompareParams) (*CompareResult, error) {
	if e.embedder == nil || e.vectorStore == nil {
		return nil, fmt.Errorf("weave: compare needs an embedder and a vector store")
	}
	topK := p.TopK
	if topK <= 0 {
		topK = e.config.DefaultTopK
	}
	window := max(3*topK, 50)
	comps := e.Components()
	res := &CompareResult{Window: window, Score: comps.Score}

	// Raw side.
	vecStart := time.Now()
	embedded, err := e.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("weave: embed query: %w", err)
	}
	if len(embedded) == 0 {
		return nil, fmt.Errorf("weave: embed query: no vector returned")
	}
	// The same scope rules as Retrieve, including the context tenant when no
	// explicit filter is given.
	scope := &RetrieveParams{CollectionID: p.CollectionID.String(), TenantID: weave.TenantFromContext(ctx), TenantFilter: p.Tenant}
	filter, tenantKey := searchScope(scope)
	raw, err := e.vectorStore.Search(ctx, embedded[0].Vector, &vectorstore.SearchOptions{TopK: window, Filter: filter, TenantKey: tenantKey})
	if err != nil {
		return nil, fmt.Errorf("weave: raw vector search: %w", err)
	}
	res.VectorMillis = float64(time.Since(vecStart).Microseconds()) / 1000
	res.VectorMatches = len(raw)
	if len(raw) > 0 {
		res.BestVectorScore = raw[0].Score
	}
	rawRank := make(map[string]int, len(raw))
	rawScore := make(map[string]float64, len(raw))
	for i, sr := range raw {
		rawRank[sr.ID] = i + 1
		rawScore[sr.ID] = sr.Score
	}

	// Final side.
	var final []ScoredChunk
	if e.retriever == nil {
		for _, sr := range raw {
			if p.MinScore > 0 && sr.Score < p.MinScore {
				continue
			}
			final = append(final, ScoredChunk{Chunk: retriever.ChunkFromSearchResult(sr), Score: sr.Score})
			if len(final) == topK {
				break
			}
		}
		res.RetrieverMillis = res.VectorMillis
	} else {
		params := &RetrieveParams{CollectionID: p.CollectionID.String(), TopK: topK, MinScore: p.MinScore, TenantFilter: p.Tenant}
		retStart := time.Now()
		final, err = e.retrieveRaw(ctx, query, params)
		if err != nil {
			return nil, err
		}
		res.RetrieverMillis = float64(time.Since(retStart).Microseconds()) / 1000
	}

	inFinal := make(map[string]bool, len(final))
	for i, h := range final {
		key := h.Chunk.ID.String()
		inFinal[key] = true
		ch := CompareHit{ScoredChunk: h, Rank: i + 1, VectorRank: rawRank[key], VectorScore: rawScore[key]}
		if ch.VectorRank != ch.Rank {
			res.Reordered = true
		}
		res.Hits = append(res.Hits, ch)
	}
	if e.retriever != nil && comps.Retriever.Kind != "similarity" {
		res.Reordered = true
	}

	if res.Reordered {
		for i, sr := range raw {
			if inFinal[sr.ID] || len(res.LeftOut) == topK {
				continue
			}
			res.LeftOut = append(res.LeftOut, CompareHit{
				ScoredChunk: ScoredChunk{Chunk: retriever.ChunkFromSearchResult(sr), Score: sr.Score},
				VectorRank:  i + 1, VectorScore: sr.Score,
			})
		}
	}

	if err := e.hydrateCompare(ctx, res.Hits); err != nil {
		return nil, err
	}
	if err := e.hydrateCompare(ctx, res.LeftOut); err != nil {
		return nil, err
	}
	return res, nil
}

func (e *Engine) hydrateCompare(ctx context.Context, hits []CompareHit) error {
	plain := make([]ScoredChunk, len(hits))
	for i := range hits {
		plain[i] = hits[i].ScoredChunk
	}
	plain, err := e.hydrate(ctx, plain)
	if err != nil {
		return err
	}
	for i := range hits {
		hits[i].ScoredChunk = plain[i]
	}
	return nil
}
