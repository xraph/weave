package api

import (
	"fmt"
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

func (a *API) createCollection(ctx forge.Context, req *CreateCollectionRequest) (*collection.Collection, error) {
	if req.Name == "" {
		return nil, forge.BadRequest("name is required")
	}

	col := &collection.Collection{
		Name:           req.Name,
		Description:    req.Description,
		EmbeddingModel: req.EmbeddingModel,
		EmbeddingDims:  req.EmbeddingDims,
		ChunkStrategy:  req.ChunkStrategy,
		ChunkSize:      req.ChunkSize,
		ChunkOverlap:   req.ChunkOverlap,
		Metadata:       req.Metadata,
	}

	if err := a.eng.CreateCollection(ctx.Context(), col); err != nil {
		return nil, fmt.Errorf("create collection: %w", err)
	}

	return col, ctx.JSON(http.StatusCreated, col)
}

func (a *API) getCollection(ctx forge.Context, _ *GetCollectionRequest) (*collection.Collection, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	col, err := a.eng.GetCollection(ctx.Context(), colID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	return col, ctx.JSON(http.StatusOK, col)
}

func (a *API) listCollections(ctx forge.Context, req *ListCollectionsRequest) ([]*collection.Collection, error) {
	cols, err := a.eng.ListCollections(ctx.Context(), &collection.ListFilter{
		Limit:  defaultLimit(req.Limit),
		Offset: req.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}

	return cols, ctx.JSON(http.StatusOK, cols)
}

func (a *API) deleteCollection(ctx forge.Context, _ *DeleteCollectionRequest) (*struct{}, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	if err := a.eng.DeleteCollection(ctx.Context(), colID); err != nil {
		return nil, mapStoreError(err)
	}

	return nil, ctx.NoContent(http.StatusNoContent)
}

func (a *API) collectionStats(ctx forge.Context, _ *CollectionStatsRequest) (*engine.CollectionStatsResult, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	stats, err := a.eng.CollectionStats(ctx.Context(), colID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	return stats, ctx.JSON(http.StatusOK, stats)
}

func (a *API) reindexCollection(ctx forge.Context, _ *ReindexCollectionRequest) (*struct{}, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	if err := a.eng.ReindexCollection(ctx.Context(), colID); err != nil {
		return nil, mapStoreError(err)
	}

	return nil, ctx.NoContent(http.StatusNoContent)
}
