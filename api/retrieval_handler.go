package api

import (
	"fmt"
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

func (a *API) retrieve(ctx forge.Context, req *RetrieveRequest) ([]engine.ScoredChunk, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	if req.Query == "" {
		return nil, forge.BadRequest("query is required")
	}

	results, err := a.eng.Retrieve(ctx.Context(), req.Query,
		engine.WithCollection(colID),
		engine.WithTopK(req.TopK),
		engine.WithMinScore(req.MinScore),
		engine.WithStrategy(req.Strategy),
	)
	if err != nil {
		return nil, fmt.Errorf("retrieve: %w", err)
	}

	return results, ctx.JSON(http.StatusOK, results)
}

func (a *API) hybridSearch(ctx forge.Context, req *HybridSearchRequest) ([]engine.ScoredChunk, error) {
	if req.Query == "" {
		return nil, forge.BadRequest("query is required")
	}

	results, err := a.eng.HybridSearch(ctx.Context(), req.Query, &engine.HybridSearchParams{
		Collections: req.Collections,
		TopK:        req.TopK,
		Strategy:    req.Strategy,
		MinScore:    req.MinScore,
	})
	if err != nil {
		return nil, fmt.Errorf("hybrid search: %w", err)
	}

	return results, ctx.JSON(http.StatusOK, results)
}
