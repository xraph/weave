package contract

import (
	"context"
	"strings"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store/memory"
)

func TestRetrievalRunAndAssemble(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		p := principal()
		col := mustCollection(t, deps, "kb")
		want := mustIngest(t, ctx, deps, col.ID, "refunds", "refunds are issued within thirty days")
		mustIngest(t, ctx, deps, col.ID, "shipping", "shipping takes five working days")

		out, err := retrievalRunHandler(deps)(ctx, runInput{Query: "refunds thirty days", CollectionID: col.ID.String(), TopK: 2}, p)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if len(out.Result.Hits) == 0 || out.Result.Hits[0].Chunk.DocumentID.String() != want.String() || !out.Result.SameSearch {
			t.Fatalf("hits: %+v", out.Result)
		}
		if out.Context == nil || !strings.Contains(out.Context.Context, "refunds are issued") || out.Context.MaxTokens != defaultMaxTokens {
			t.Fatalf("context: %+v", out.Context)
		}

		refs := []engine.ChunkRef{{ChunkID: out.Result.Hits[0].Chunk.ID, Score: out.Result.Hits[0].Score}}
		ac, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: refs, MaxTokens: 5}, p)
		if err != nil || ac.MaxTokens != 5 || ac.FirstExcluded != 0 {
			t.Fatalf("assemble with a tiny budget: %+v %v", ac, err)
		}
	})
}

func TestRetrievalRun_TenantFilter(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	col := mustCollection(t, deps, "kb")
	mustIngest(t, weave.WithTenant(ctx, "t1"), deps, col.ID, "tenanted", "refunds for tenant one")
	open := mustIngest(t, ctx, deps, col.ID, "open", "refunds for nobody in particular")

	empty := ""
	out, err := retrievalRunHandler(deps)(ctx, runInput{Query: "refunds", Tenant: &empty}, principal())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Result.Hits) != 1 || out.Result.Hits[0].Chunk.DocumentID.String() != open.String() {
		t.Errorf("tenant \"\": %+v", out.Result.Hits)
	}
}

func TestRetrievalRun_Validation(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	cases := map[string]runInput{
		"blank query":     {Query: "   "},
		"huge query":      {Query: strings.Repeat("q", maxQueryBytes+1)},
		"negative top_k":  {Query: "x", TopK: -1},
		"negative budget": {Query: "x", MaxTokens: -1},
		"bad collection":  {Query: "x", CollectionID: "nope"},
	}
	for name, in := range cases {
		if _, err := retrievalRunHandler(deps)(ctx, in, principal()); codeOf(err) != dashcontract.CodeBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: []engine.ChunkRef{{ChunkID: id.NewChunkID()}}}, principal()); codeOf(err) != dashcontract.CodeNotFound {
		t.Errorf("stale ref: %v", err)
	}
	tooMany := make([]engine.ChunkRef, maxAssembleRefs+1)
	if _, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: tooMany}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("too many refs: %v", err)
	}
}

// A deployment with no embedder cannot search; the page must say so.
func TestRetrievalRun_NoEmbedderIsUnavailable(t *testing.T) {
	e, err := engine.New(engine.WithStore(memory.New()), engine.WithChunker(chunker.NewRecursiveChunker()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = retrievalRunHandler(Deps{Engine: e})(context.Background(), runInput{Query: "anything"}, principal())
	if codeOf(err) != dashcontract.CodeUnavailable {
		t.Errorf("no embedder: %v", err)
	}
}
