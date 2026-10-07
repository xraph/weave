package engine_test

import (
	"context"
	"strconv"
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

func TestRetrieveKeepsVectorMetadataAndOwnsItsChunk(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "metadata")
	mustIngest(t, ctx, r.Engine, col.ID, "refunds", "refunds are issued within thirty days")

	hits, err := r.Engine.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID))
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(hits) == 0 || !hits[0].Hydrated {
		t.Fatalf("retrieve: want a hydrated top hit, got %+v", hits)
	}
	top := hits[0].Chunk
	want := map[string]string{
		"document_id":   top.DocumentID.String(),
		"collection_id": top.CollectionID.String(),
		"chunk_index":   strconv.Itoa(top.Index),
		"tenant_id":     top.TenantID,
	}
	for k, v := range want {
		got, ok := top.Metadata[k]
		if !ok || got != v {
			t.Errorf("metadata[%q]: got %q (present %v), want %q", k, got, ok, v)
		}
	}

	stored, err := r.Engine.GetChunk(ctx, top.ID)
	if err != nil {
		t.Fatalf("get chunk: %v", err)
	}
	original := stored.Content
	top.Content = "changed by the caller"
	again, err := r.Engine.GetChunk(ctx, top.ID)
	if err != nil {
		t.Fatalf("get chunk again: %v", err)
	}
	if again.Content != original {
		t.Errorf("stored content: got %q after editing a hit, want %q (the hit shares the store's chunk)", again.Content, original)
	}
}

func TestRetrieveOrphanKeepsItsPlace(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "mixed")
	mustIngest(t, ctx, r.Engine, col.ID, "refunds", "refunds are issued within thirty days")
	mustIngest(t, ctx, r.Engine, col.ID, "late refunds", "late refunds take longer")

	before, err := r.Engine.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID), engine.WithTopK(2))
	if err != nil {
		t.Fatalf("retrieve before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("retrieve before: got %d hits, want 2", len(before))
	}
	order := []string{before[0].Chunk.ID.String(), before[1].Chunk.ID.String()}
	// Orphan the top hit, so keeping its place is not the same as sorting
	// orphans last.
	gone := before[0].Chunk.DocumentID
	if err := r.Store.DeleteChunksByDocument(ctx, gone); err != nil {
		t.Fatalf("delete chunks: %v", err)
	}

	after, err := r.Engine.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID), engine.WithTopK(2))
	if err != nil {
		t.Fatalf("retrieve after: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("retrieve after: got %d hits, want both kept", len(after))
	}
	for i, h := range after {
		if got := h.Chunk.ID.String(); got != order[i] {
			t.Errorf("hit %d: chunk %s, want %s (the order moved)", i, got, order[i])
		}
	}
	orphan, kept := after[0], after[1]
	if !orphan.Orphaned || orphan.Hydrated {
		t.Errorf("orphan: orphaned=%v hydrated=%v, want orphaned only", orphan.Orphaned, orphan.Hydrated)
	}
	if orphan.Chunk.Metadata["document_id"] != gone.String() {
		t.Errorf("orphan: vector document_id %q, want %q", orphan.Chunk.Metadata["document_id"], gone)
	}
	if orphan.Chunk.Content == "" {
		t.Error("orphan: lost its content")
	}
	if !kept.Hydrated || kept.Orphaned {
		t.Errorf("kept hit: hydrated=%v orphaned=%v, want hydrated only", kept.Hydrated, kept.Orphaned)
	}
}
