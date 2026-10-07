package engine_test

import (
	"context"
	"testing"

	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/retriever"
)

// The three texts are chosen so the arithmetic is checkable by hand with the
// bag-of-words embedder: A and B are near-duplicates (cosine about 0.94),
// so MMR at lambda 0.3 takes A, then prefers C over B for diversity.
const (
	textA = "refund policy refund window thirty days"
	textB = "refund policy refund window thirty days please"
	textC = "shipping refund label return"
)

func TestRetrieveCompareShowsMMRMovement(t *testing.T) {
	e := newRig(t, func(r *testRig) engine.Option {
		return engine.WithRetriever(retriever.NewMMRRetriever(r.Vectors, r.Embed, 0.3))
	}).Engine
	ctx := context.Background()
	col := mustTestCollection(t, e, "compare")
	a := mustIngest(t, ctx, e, col.ID, "a", textA)
	b := mustIngest(t, ctx, e, col.ID, "b", textB)
	c := mustIngest(t, ctx, e, col.ID, "c", textC)

	res, err := e.RetrieveCompare(ctx, "refund policy window", engine.CompareParams{CollectionID: col.ID, TopK: 2})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !res.Reordered {
		t.Error("reordered: got false for an MMR retriever")
	}
	if len(res.Hits) != 2 {
		t.Fatalf("hits: got %d, want 2", len(res.Hits))
	}
	if res.Hits[0].Chunk.DocumentID.String() != a.String() || res.Hits[1].Chunk.DocumentID.String() != c.String() {
		t.Errorf("final order: got %s, %s; want a then c", res.Hits[0].Chunk.DocumentID, res.Hits[1].Chunk.DocumentID)
	}
	if res.Hits[1].Rank != 2 || res.Hits[1].VectorRank != 3 {
		t.Errorf("c: rank %d vector rank %d, want 2 and 3", res.Hits[1].Rank, res.Hits[1].VectorRank)
	}
	if len(res.LeftOut) != 1 || res.LeftOut[0].Chunk.DocumentID.String() != b.String() || res.LeftOut[0].VectorRank != 2 {
		t.Errorf("left out: got %+v, want b at vector rank 2", res.LeftOut)
	}
	if res.Window < 50 || res.VectorMatches != 3 {
		t.Errorf("window %d, vector matches %d", res.Window, res.VectorMatches)
	}
}

func TestRetrieveCompareWithoutRetriever(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "plain")
	mustIngest(t, ctx, r.Engine, col.ID, "a", textA)
	mustIngest(t, ctx, r.Engine, col.ID, "c", textC)

	res, err := r.Engine.RetrieveCompare(ctx, "refund policy window", engine.CompareParams{CollectionID: col.ID, TopK: 1})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if res.Reordered || len(res.LeftOut) != 0 {
		t.Errorf("plain vector search: reordered %v, left out %d; want neither", res.Reordered, len(res.LeftOut))
	}
	if len(res.Hits) != 1 || res.Hits[0].Rank != 1 || res.Hits[0].VectorRank != 1 {
		t.Errorf("hits: %+v", res.Hits)
	}
}

func TestRetrieveCompareExplainsAnEmptyResult(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "strict")
	mustIngest(t, ctx, r.Engine, col.ID, "c", textC)

	res, err := r.Engine.RetrieveCompare(ctx, "refund policy window", engine.CompareParams{CollectionID: col.ID, TopK: 5, MinScore: 0.99})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(res.Hits) != 0 {
		t.Fatalf("hits: got %d, want none above 0.99", len(res.Hits))
	}
	if res.VectorMatches != 1 || res.BestVectorScore <= 0 || res.BestVectorScore >= 0.99 {
		t.Errorf("explanation: vector matches %d, best %.3f; want 1 match below the minimum", res.VectorMatches, res.BestVectorScore)
	}
}
