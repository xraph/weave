package contract

import (
	"context"

	"github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

// overviewNewest is how many of the newest documents the Overview lists.
const overviewNewest = 10

type overviewOutput struct {
	Collections         int64             `json:"collections"`
	Documents           int64             `json:"documents"`
	DocumentsByState    stateCounts       `json:"documents_by_state"`
	Chunks              int64             `json:"chunks"`
	Stalled             int64             `json:"stalled"`
	StalledAfterSeconds int               `json:"stalled_after_seconds"`
	NewestDocuments     []documentRow     `json:"newest_documents"`
	Components          engine.Components `json:"components"`
	// Scope is "all": the dashboard path resolves no tenant, so it sees
	// every tenant's data unless a page filters by one.
	Scope string `json:"scope"`
}

func systemOverviewHandler(deps Deps) func(context.Context, tenantInput, contract.Principal) (overviewOutput, error) {
	return func(ctx context.Context, in tenantInput, _ contract.Principal) (overviewOutput, error) {
		const intent = "system.overview"
		e := deps.Engine
		out := overviewOutput{StalledAfterSeconds: int(StalledAfter.Seconds()), Scope: "all", NewestDocuments: []documentRow{}, Components: e.Components()}
		var err error
		if out.Collections, err = e.CountCollections(ctx, &collection.CountFilter{Tenant: in.Tenant}); err != nil {
			return overviewOutput{}, deps.mapError(intent, err)
		}
		if out.Documents, err = e.CountDocuments(ctx, &document.CountFilter{Tenant: in.Tenant}); err != nil {
			return overviewOutput{}, deps.mapError(intent, err)
		}
		if out.DocumentsByState, err = countStates(ctx, e, id.Nil, in.Tenant); err != nil {
			return overviewOutput{}, deps.mapError(intent, err)
		}
		if out.Chunks, err = e.CountChunks(ctx, &chunk.CountFilter{Tenant: in.Tenant}); err != nil {
			return overviewOutput{}, deps.mapError(intent, err)
		}
		if out.Stalled, err = e.CountDocuments(ctx, &document.CountFilter{
			State: document.StateProcessing, UpdatedBefore: deps.now().Add(-StalledAfter), Tenant: in.Tenant,
		}); err != nil {
			return overviewOutput{}, deps.mapError(intent, err)
		}
		docs, err := e.ListDocuments(ctx, &document.ListFilter{SortDesc: true, Limit: overviewNewest, Tenant: in.Tenant})
		if err != nil {
			return overviewOutput{}, deps.mapError(intent, err)
		}
		cache := newNameCache(e)
		for _, d := range docs {
			row, err := deps.documentRow(ctx, cache, d)
			if err != nil {
				return overviewOutput{}, deps.mapError(intent, err)
			}
			out.NewestDocuments = append(out.NewestDocuments, row)
		}
		return out, nil
	}
}

// engineConfig is the engine's configuration as the engine holds it. The
// recorded-but-unused defaults (model, strategy) are listed because they
// are what new collections record. IngestConcurrency is left out: the
// engine never reads it.
type engineConfig struct {
	DefaultChunkSize       int     `json:"default_chunk_size"`
	DefaultChunkOverlap    int     `json:"default_chunk_overlap"`
	DefaultEmbeddingModel  string  `json:"default_embedding_model"`
	DefaultChunkStrategy   string  `json:"default_chunk_strategy"`
	DefaultTopK            int     `json:"default_top_k"`
	ShutdownTimeoutSeconds float64 `json:"shutdown_timeout_seconds"`
}

type componentsOutput struct {
	Components engine.Components      `json:"components"`
	Config     engineConfig           `json:"config"`
	Extensions []engine.ExtensionInfo `json:"extensions"`
}

func systemComponentsHandler(deps Deps) func(context.Context, struct{}, contract.Principal) (componentsOutput, error) {
	return func(_ context.Context, _ struct{}, _ contract.Principal) (componentsOutput, error) {
		e := deps.Engine
		cfg := e.Config()
		return componentsOutput{
			Components: e.Components(),
			Config: engineConfig{
				DefaultChunkSize:       cfg.DefaultChunkSize,
				DefaultChunkOverlap:    cfg.DefaultChunkOverlap,
				DefaultEmbeddingModel:  cfg.DefaultEmbeddingModel,
				DefaultChunkStrategy:   cfg.DefaultChunkStrategy,
				DefaultTopK:            cfg.DefaultTopK,
				ShutdownTimeoutSeconds: cfg.ShutdownTimeout.Seconds(),
			},
			Extensions: e.DescribeExtensions(),
		}, nil
	}
}
