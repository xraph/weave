package engine_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

func hit(content string, score float64) engine.ScoredChunk {
	return engine.ScoredChunk{Chunk: &chunk.Chunk{Content: content}, Score: score}
}

// The assembler skips a chunk that does not fit and keeps going, so a later,
// smaller chunk can still get in. The result must say exactly which ones did.
func TestAssembleSkipsAndContinues(t *testing.T) {
	e := newTestEngine(t)
	hits := []engine.ScoredChunk{
		hit(strings.Repeat("a", 20), 0.9), // 5 tokens
		hit(strings.Repeat("b", 40), 0.8), // 10 tokens: does not fit after the first
		hit(strings.Repeat("c", 16), 0.7), // 4 tokens: fits
	}
	got, err := e.Assemble(context.Background(), hits, engine.AssembleParams{MaxTokens: 10})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if !slices.Equal(got.Included, []int{0, 2}) || got.FirstExcluded != 1 {
		t.Errorf("included %v first excluded %d, want [0 2] and 1", got.Included, got.FirstExcluded)
	}
	if got.TotalTokens != 9 || got.MaxTokens != 10 || got.TokenCounter != "chars/4" {
		t.Errorf("totals: %+v", got)
	}
	if !strings.Contains(got.Context, "[1] aaaa") || !strings.Contains(got.Context, "[2] cccc") || strings.Contains(got.Context, "bbbb") {
		t.Errorf("context: %q", got.Context)
	}

	all, err := e.Assemble(context.Background(), hits, engine.AssembleParams{})
	if err != nil {
		t.Fatalf("assemble default budget: %v", err)
	}
	if all.FirstExcluded != -1 || all.MaxTokens != 4096 {
		t.Errorf("default budget: first excluded %d, max %d", all.FirstExcluded, all.MaxTokens)
	}
}

func TestAssembleRefsReadsChunksBack(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "refs")
	mustIngest(t, ctx, r.Engine, col.ID, "refunds", "refunds are issued within thirty days")
	chunks, err := r.Engine.ListChunks(ctx, &chunk.ListFilter{CollectionID: col.ID})
	if err != nil || len(chunks) != 1 {
		t.Fatalf("list chunks: %v (%d)", err, len(chunks))
	}

	got, hits, err := r.Engine.AssembleRefs(ctx, []engine.ChunkRef{{ChunkID: chunks[0].ID, Score: 0.8}}, engine.AssembleParams{MaxTokens: 100})
	if err != nil {
		t.Fatalf("assemble refs: %v", err)
	}
	if !strings.Contains(got.Context, "refunds are issued") || len(hits) != 1 || hits[0].Score != 0.8 || !hits[0].Hydrated {
		t.Errorf("assemble refs: %q, %+v", got.Context, hits)
	}

	_, _, err = r.Engine.AssembleRefs(ctx, []engine.ChunkRef{{ChunkID: id.NewChunkID()}}, engine.AssembleParams{})
	if !errors.Is(err, weave.ErrChunkNotFound) {
		t.Errorf("missing chunk: got %v, want ErrChunkNotFound", err)
	}
}

// TestAssembleNilChunks tests that nil Chunks are handled gracefully without panicking.
// A nil-Chunk hit should never appear in Included and can be FirstExcluded.
func TestAssembleNilChunks(t *testing.T) {
	e := newTestEngine(t)
	hits := []engine.ScoredChunk{
		{Chunk: nil, Score: 0.9},                       // nil chunk, skipped
		hit(strings.Repeat("x", 16), 0.8),             // 4 tokens: fits
	}
	got, err := e.Assemble(context.Background(), hits, engine.AssembleParams{MaxTokens: 100})
	if err != nil {
		t.Fatalf("assemble with nil chunk: %v", err)
	}
	if !slices.Equal(got.Included, []int{1}) || got.FirstExcluded != 0 {
		t.Errorf("included %v first excluded %d, want [1] and 0", got.Included, got.FirstExcluded)
	}
	if !strings.Contains(got.Context, "xxxx") {
		t.Errorf("context: %q, want to contain xxxx", got.Context)
	}
}
