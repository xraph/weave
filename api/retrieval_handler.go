package api

import (
	"fmt"
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

// maxTopK caps top_k on the retrieve and search routes, as the list routes
// cap limit.
const maxTopK = 1000

func (a *API) retrieve(ctx forge.Context, req *RetrieveRequest) (*struct{}, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	if req.Query == "" {
		return nil, forge.BadRequest("query is required")
	}

	results, err := a.eng.Retrieve(ctx.Context(), req.Query,
		engine.WithCollection(colID),
		engine.WithTopK(min(req.TopK, maxTopK)),
		engine.WithMinScore(req.MinScore),
		engine.WithStrategy(req.Strategy),
	)
	if err != nil {
		return nil, mapStoreError(err)
	}

	return nil, ctx.JSON(http.StatusOK, results)
}

func (a *API) hybridSearch(ctx forge.Context, req *HybridSearchRequest) (*struct{}, error) {
	if req.Query == "" {
		return nil, forge.BadRequest("query is required")
	}

	results, err := a.eng.HybridSearch(ctx.Context(), req.Query, &engine.HybridSearchParams{
		Collections: req.Collections,
		TopK:        min(req.TopK, maxTopK),
		Strategy:    req.Strategy,
		MinScore:    req.MinScore,
	})
	if err != nil {
		return nil, mapStoreError(err)
	}

	return nil, ctx.JSON(http.StatusOK, results)
}
