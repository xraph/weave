package contract

import (
	"context"
	"errors"
	"strings"

	"github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/engine"
)

const (
	maxQueryBytes    = 8 << 10
	maxTopK          = 50
	defaultMaxTokens = 4096
	maxMaxTokens     = 32768
	maxAssembleRefs  = 50
)

type runInput struct {
	Query        string  `json:"query"`
	CollectionID string  `json:"collection_id"`
	Tenant       *string `json:"tenant"`
	TopK         int     `json:"top_k"`
	MinScore     float64 `json:"min_score"`
	MaxTokens    int     `json:"max_tokens"`
}

// runOutput is one retrieval run: the configured ranking beside the raw
// vector ranking, and what Weave's default assembler builds from it.
type runOutput struct {
	Result  *engine.CompareResult    `json:"result"`
	Context *engine.AssembledContext `json:"context"`
}

type assembleInput struct {
	Hits      []engine.ChunkRef `json:"hits"`
	MaxTokens int               `json:"max_tokens"`
}

// resolveBudget turns the requested token budget into the one the
// assembler gets: zero is the default, an oversized request is clamped.
func resolveBudget(n int) (int, error) {
	switch {
	case n < 0:
		return 0, badRequest("max_tokens cannot be negative")
	case n == 0:
		return defaultMaxTokens, nil
	case n > maxMaxTokens:
		return maxMaxTokens, nil
	}
	return n, nil
}

func retrievalRunHandler(deps Deps) func(context.Context, runInput, contract.Principal) (runOutput, error) {
	return func(ctx context.Context, in runInput, _ contract.Principal) (runOutput, error) {
		const intent = "retrieval.run"
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return runOutput{}, badRequest("query is empty")
		}
		if len(in.Query) > maxQueryBytes {
			return runOutput{}, badRequest("query is longer than 8 KiB")
		}
		if in.TopK < 0 {
			return runOutput{}, badRequest("top_k cannot be negative")
		}
		topK := in.TopK
		if topK == 0 {
			topK = deps.Engine.Config().DefaultTopK
		}
		topK = min(topK, maxTopK)
		budget, err := resolveBudget(in.MaxTokens)
		if err != nil {
			return runOutput{}, err
		}
		colID, err := optionalCollectionID(in.CollectionID)
		if err != nil {
			return runOutput{}, err
		}
		comps := deps.Engine.Components()
		if !comps.Embedder.Configured {
			return runOutput{}, mapError(weave.ErrNoEmbedder)
		}
		if !comps.VectorStore.Configured {
			return runOutput{}, mapError(weave.ErrNoVectorStore)
		}

		res, err := deps.Engine.RetrieveCompare(ctx, query, engine.CompareParams{CollectionID: colID, Tenant: in.Tenant, TopK: topK, MinScore: in.MinScore})
		if err != nil {
			return runOutput{}, deps.mapError(intent, err)
		}
		hits := make([]engine.ScoredChunk, len(res.Hits))
		for i, h := range res.Hits {
			hits[i] = h.ScoredChunk
		}
		ac, err := deps.Engine.Assemble(ctx, hits, engine.AssembleParams{MaxTokens: budget})
		if err != nil {
			return runOutput{}, deps.mapError(intent, err)
		}
		return runOutput{Result: res, Context: ac}, nil
	}
}

// retrievalAssembleHandler re-assembles an earlier ranking with a new
// budget. It reads each chunk back by ID and never embeds again. A chunk
// that has gone means the ranking is stale.
func retrievalAssembleHandler(deps Deps) func(context.Context, assembleInput, contract.Principal) (*engine.AssembledContext, error) {
	return func(ctx context.Context, in assembleInput, _ contract.Principal) (*engine.AssembledContext, error) {
		if len(in.Hits) > maxAssembleRefs {
			return nil, badRequest("at most 50 hits can be assembled at once")
		}
		budget, err := resolveBudget(in.MaxTokens)
		if err != nil {
			return nil, err
		}
		ac, _, err := deps.Engine.AssembleRefs(ctx, in.Hits, engine.AssembleParams{MaxTokens: budget})
		if errors.Is(err, weave.ErrChunkNotFound) {
			return nil, notFound("a chunk in this ranking no longer exists; run the query again")
		}
		if err != nil {
			return nil, deps.mapError("retrieval.assemble", err)
		}
		return ac, nil
	}
}
