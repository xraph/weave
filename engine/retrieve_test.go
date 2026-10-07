package engine_test

import (
	"context"
	"testing"

	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/retriever"
)

func TestRetrieveHitsCarryIdentity(t *testing.T) {
	configs := map[string][]rigOption{
		"no retriever": nil,
		"similarity": {func(r *testRig) engine.Option {
			return engine.WithRetriever(retriever.NewSimilarityRetriever(r.Vectors, r.Embed, r.Store))
		}},
		"mmr": {func(r *testRig) engine.Option {
			return engine.WithRetriever(retriever.NewMMRRetriever(r.Vectors, r.Embed, 0.7))
		}},
		"hybrid": {func(r *testRig) engine.Option {
			return engine.WithRetriever(retriever.NewHybridRetriever(retriever.NewSimilarityRetriever(r.Vectors, r.Embed, r.Store)))
		}},
	}
	for name, extra := range configs {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, extra...)
			ctx := context.Background()
			col := mustTestCollection(t, r.Engine, "identity")
			mustIngest(t, ctx, r.Engine, col.ID, "shipping", "shipping takes five working days")
			want := mustIngest(t, ctx, r.Engine, col.ID, "refunds", "refunds are issued within thirty days")

			hits, err := r.Engine.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID), engine.WithTopK(2))
			if err != nil {
				t.Fatalf("retrieve: %v", err)
			}
			if len(hits) == 0 {
				t.Fatal("retrieve: no hits")
			}
			top := hits[0]
			if !top.Hydrated || top.Orphaned {
				t.Errorf("top hit: hydrated=%v orphaned=%v, want hydrated and not orphaned", top.Hydrated, top.Orphaned)
			}
			if top.Chunk.ID.String() == "" {
				t.Error("top hit: empty chunk ID")
			}
			if top.Chunk.DocumentID.String() != want.String() {
				t.Errorf("top hit: document %q, want %q", top.Chunk.DocumentID, want)
			}
			if top.Chunk.CollectionID.String() != col.ID.String() {
				t.Errorf("top hit: collection %q, want %q", top.Chunk.CollectionID, col.ID)
			}
			if top.Chunk.EndOffset == 0 || top.Chunk.TokenCount == 0 {
				t.Errorf("top hit: offsets/tokens not hydrated: %+v", top.Chunk)
			}
		})
	}
}

func TestRetrieveMarksOrphans(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "orphans")
	doc := mustIngest(t, ctx, r.Engine, col.ID, "refunds", "refunds are issued within thirty days")

	// Remove the chunk rows and leave the vectors, which is what a failed
	// ingest leaves behind in the opposite order.
	if err := r.Store.DeleteChunksByDocument(ctx, doc); err != nil {
		t.Fatalf("delete chunks: %v", err)
	}

	hits, err := r.Engine.Retrieve(ctx, "refunds", engine.WithCollection(col.ID))
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("retrieve: got %d hits, want the orphan kept", len(hits))
	}
	if !hits[0].Orphaned || hits[0].Hydrated {
		t.Errorf("orphan: orphaned=%v hydrated=%v", hits[0].Orphaned, hits[0].Hydrated)
	}
	if hits[0].Chunk.ID.String() == "" || hits[0].Chunk.Content == "" {
		t.Errorf("orphan: kept no ID or content: %+v", hits[0].Chunk)
	}
}
