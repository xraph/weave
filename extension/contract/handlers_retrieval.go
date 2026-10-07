package contract

import (
	"context"
	"strings"

	"github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

const (
	maxQueryBytes    = 8 << 10
	maxTopK          = 50
	defaultMaxTokens = 4096
	maxMaxTokens     = 32768
	maxAssembleRefs  = 50
	maxAssembleBytes = 1 << 20
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

// assembleHit is one hit of an earlier run, echoed back exactly as the run
// returned it. chunk_id is "" for a hit a custom retriever did not identify.
// content is null for a hit that had no chunk at all: the run skipped it,
// so re-assembly skips it too.
type assembleHit struct {
	ChunkID string  `json:"chunk_id"`
	Content *string `json:"content"`
	Score   float64 `json:"score"`
}

type assembleInput struct {
	Hits      []assembleHit `json:"hits"`
	MaxTokens int           `json:"max_tokens"`
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
			return runOutput{}, unavailable("retrieval needs Weave's own embedder and vector store; this deployment has no embedder configured")
		}
		if !comps.VectorStore.Configured {
			return runOutput{}, unavailable("retrieval needs Weave's own embedder and vector store; this deployment has no vector store configured")
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

// retrievalAssembleHandler re-assembles exactly the hits it is sent, so the
// same hits and budget always give the context retrieval.run gave. That
// includes an orphaned hit (a vector whose chunk row is gone), a hit with
// no chunk ID and a hit with no chunk at all. It never embeds and never
// reads the store.
func retrievalAssembleHandler(deps Deps) func(context.Context, assembleInput, contract.Principal) (*engine.AssembledContext, error) {
	return func(ctx context.Context, in assembleInput, _ contract.Principal) (*engine.AssembledContext, error) {
		if len(in.Hits) > maxAssembleRefs {
			return nil, badRequest("at most 50 hits can be assembled at once")
		}
		total := 0
		for _, h := range in.Hits {
			if h.Content != nil {
				total += len(*h.Content)
			}
		}
		if total > maxAssembleBytes {
			return nil, badRequest("the hits hold more than 1 MiB of text in total")
		}
		budget, err := resolveBudget(in.MaxTokens)
		if err != nil {
			return nil, err
		}
		hits := make([]engine.ScoredChunk, len(in.Hits))
		for i, h := range in.Hits {
			if h.Content == nil {
				// The run's hit had no chunk, and Engine.Assemble
				// skipped it. A nil chunk here skips it the same way.
				hits[i] = engine.ScoredChunk{Score: h.Score}
				continue
			}
			cid, perr := id.ParseChunkID(h.ChunkID)
			if perr != nil {
				cid = id.Nil
			}
			hits[i] = engine.ScoredChunk{Chunk: &chunk.Chunk{ID: cid, Content: *h.Content}, Score: h.Score}
		}
		ac, err := deps.Engine.Assemble(ctx, hits, engine.AssembleParams{MaxTokens: budget})
		if err != nil {
			return nil, deps.mapError("retrieval.assemble", err)
		}
		return ac, nil
	}
}
