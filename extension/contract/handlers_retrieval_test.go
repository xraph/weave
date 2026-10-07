package contract

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/store/memory"
)

// echo returns a run's hits the way the page sends them back to
// retrieval.assemble: in order, as {chunk_id, content, score}, with null
// content for a hit that had no chunk.
func echo(r *engine.CompareResult) []assembleHit {
	hits := make([]assembleHit, len(r.Hits))
	for i, h := range r.Hits {
		hits[i] = assembleHit{Score: h.Score}
		if h.Chunk == nil {
			continue
		}
		hits[i].Content = text(h.Chunk.Content)
		hits[i].ChunkID = h.Chunk.ID.String()
	}
	return hits
}

func text(s string) *string { return &s }

func sameContext(t *testing.T, label string, got, want *engine.AssembledContext) {
	t.Helper()
	if got.Context != want.Context || got.TotalTokens != want.TotalTokens || got.MaxTokens != want.MaxTokens ||
		got.FirstExcluded != want.FirstExcluded || !slices.Equal(got.Included, want.Included) {
		t.Errorf("%s: assemble %+v differs from run %+v", label, got, want)
	}
}

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

		ac, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: echo(out.Result), MaxTokens: 5}, p)
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

// A deployment with an embedder but no vector store cannot search either.
func TestRetrievalRun_NoVectorStoreIsUnavailable(t *testing.T) {
	e, err := engine.New(engine.WithStore(memory.New()), engine.WithChunker(chunker.NewRecursiveChunker()), engine.WithEmbedder(hashEmbedder{dims: 8}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = retrievalRunHandler(Deps{Engine: e})(context.Background(), runInput{Query: "anything"}, principal())
	if codeOf(err) != dashcontract.CodeUnavailable || !strings.Contains(err.Error(), "vector store") {
		t.Errorf("no vector store: %v", err)
	}
}

// The assembled context must be reproducible from the hits alone: the same
// hits and budget give back exactly what the run handed the model.
func TestRetrievalAssemble_ParityWithRun(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		col := mustCollection(t, deps, "kb")
		mustIngest(t, ctx, deps, col.ID, "refunds", strings.Repeat("refunds are issued within thirty days. ", 8))
		mustIngest(t, ctx, deps, col.ID, "returns", "returns and refunds need the original receipt")
		mustIngest(t, ctx, deps, col.ID, "shipping", "shipping takes five working days")

		out, err := retrievalRunHandler(deps)(ctx, runInput{Query: "refunds", MaxTokens: 60}, principal())
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		ac, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: echo(out.Result), MaxTokens: 60}, principal())
		if err != nil {
			t.Fatalf("assemble: %v", err)
		}
		sameContext(t, "parity", ac, out.Context)
	})
}

// An orphan is a vector whose chunk row is gone. The run still hands its
// text to the model, so assemble must reproduce it rather than refuse.
func TestRetrievalAssemble_OrphanParity(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	col := mustCollection(t, deps, "kb")
	mustIngest(t, ctx, deps, col.ID, "keep", "refunds are issued within thirty days")
	gone := mustIngest(t, ctx, deps, col.ID, "gone", "refunds orphan text lives only in the vector")
	if err := deps.Engine.Store().DeleteChunksByDocument(ctx, gone); err != nil {
		t.Fatalf("delete chunks: %v", err)
	}

	out, err := retrievalRunHandler(deps)(ctx, runInput{Query: "refunds", MaxTokens: 200}, principal())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	orphans := 0
	for _, h := range out.Result.Hits {
		if h.Orphaned {
			orphans++
			if !strings.Contains(out.Context.Context, h.Chunk.Content) {
				t.Errorf("orphan text missing from the run's context: %q", h.Chunk.Content)
			}
		}
	}
	if orphans != 1 {
		t.Fatalf("want one orphaned hit, got %d: %+v", orphans, out.Result.Hits)
	}
	ac, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: echo(out.Result), MaxTokens: 200}, principal())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	sameContext(t, "orphan parity", ac, out.Context)
}

func TestRetrievalAssemble_UnidentifiedHit(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ac, err := retrievalAssembleHandler(deps)(context.Background(), assembleInput{Hits: []assembleHit{{Content: text("from a custom retriever"), Score: 0.5}}}, principal())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !strings.Contains(ac.Context, "from a custom retriever") || len(ac.Included) != 1 {
		t.Errorf("context: %+v", ac)
	}
}

func TestRetrievalAssemble_Caps(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	if _, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: make([]assembleHit, maxAssembleRefs+1)}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("too many hits: %v", err)
	}
	big := []assembleHit{{Content: text(strings.Repeat("x", maxAssembleBytes/2+1))}, {Content: text(strings.Repeat("x", maxAssembleBytes/2))}}
	if _, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: big}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("too much text: %v", err)
	}
	// A null content counts nothing toward the cap.
	full := []assembleHit{{Content: text(strings.Repeat("x", maxAssembleBytes))}, {}}
	if _, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: full}, principal()); err != nil {
		t.Errorf("exactly 1 MiB plus a null hit: %v", err)
	}
	if _, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: []assembleHit{{Content: text("x")}}, MaxTokens: -1}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("negative budget: %v", err)
	}
}

// mixedRetriever returns what a careless custom retriever can: an
// identified chunk, a hit with no chunk at all, an empty chunk and an
// unidentified chunk.
type mixedRetriever struct{ keep *chunk.Chunk }

func (m mixedRetriever) Retrieve(context.Context, string, *retriever.Options) ([]retriever.Result, error) {
	return []retriever.Result{
		{Chunk: m.keep, Score: 0.9},
		{Chunk: nil, Score: 0.8},
		{Chunk: &chunk.Chunk{}, Score: 0.7},
		{Chunk: &chunk.Chunk{Content: "unidentified text from a custom retriever"}, Score: 0.6},
	}, nil
}

// A hit with a null chunk is skipped by the run's assembler. Re-assembly
// must skip it too, or the context and the markers drift.
func TestRetrievalAssemble_NullChunkParity(t *testing.T) {
	ctx := context.Background()
	seed := newDeps(t, openMemory(t))
	col := mustCollection(t, seed, "kb")
	docID := mustIngest(t, ctx, seed, col.ID, "refunds", "refunds are issued within thirty days")
	chunks, err := seed.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: docID})
	if err != nil || len(chunks) == 0 {
		t.Fatalf("chunks: %v %v", chunks, err)
	}
	deps := newDeps(t, seed.Engine.Store(), engine.WithRetriever(mixedRetriever{keep: chunks[0]}))

	out, err := retrievalRunHandler(deps)(ctx, runInput{Query: "refunds", MaxTokens: 200}, principal())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(out.Result.Hits) != 4 || out.Result.Hits[1].Chunk != nil {
		t.Fatalf("want four hits with a null second chunk: %+v", out.Result.Hits)
	}

	sent := echo(out.Result)
	if sent[1].Content != nil {
		t.Fatalf("echo sent content for a null chunk: %q", *sent[1].Content)
	}
	raw, err := json.Marshal(sent[1])
	if err != nil || !strings.Contains(string(raw), `"content":null`) {
		t.Fatalf("a null chunk must echo as content null: %s %v", raw, err)
	}

	runHits := make([]engine.ScoredChunk, len(out.Result.Hits))
	for i, h := range out.Result.Hits {
		runHits[i] = h.ScoredChunk
	}
	direct, err := deps.Engine.Assemble(ctx, runHits, engine.AssembleParams{MaxTokens: 200})
	if err != nil {
		t.Fatalf("Engine.Assemble: %v", err)
	}
	viaHandler, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: sent, MaxTokens: 200}, principal())
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	sameContext(t, "run vs Engine.Assemble", out.Context, direct)
	sameContext(t, "handler vs Engine.Assemble", viaHandler, direct)
	if direct.FirstExcluded != 1 || slices.Contains(direct.Included, 1) {
		t.Errorf("the null hit should be the first excluded and never included: %+v", direct)
	}
}
