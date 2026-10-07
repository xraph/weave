package storetest

import (
	"context"
	"testing"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
)

func testChunks(t *testing.T, s store.Store) {
	ctx := context.Background()

	a := mustCollection(t, s, "chunks-a", "t1")
	b := mustCollection(t, s, "chunks-b", "t1")
	d1 := mustDocument(t, s, a, "first", document.StateReady)
	d2 := mustDocument(t, s, a, "second", document.StateReady)
	d3 := mustDocument(t, s, b, "third", document.StateReady)
	c1 := mustChunks(t, s, d1, 3)
	c2 := mustChunks(t, s, d2, 2)
	mustChunks(t, s, d3, 1)

	byDoc, err := s.ListChunks(ctx, &chunk.ListFilter{DocumentID: d1.ID})
	if err != nil {
		t.Fatalf("list by document: %v", err)
	}
	sameOrder(t, "by document, in index order", chunkIDs(byDoc), chunkIDs(c1))

	// Every field the dashboard shows survives the round trip.
	got, want := byDoc[2], c1[2]
	if got.StartOffset != want.StartOffset || got.EndOffset != want.EndOffset ||
		got.TokenCount != want.TokenCount || got.Index != want.Index ||
		got.Content != want.Content || got.Metadata["section"] != "2" ||
		got.TenantID != "t1" || got.CollectionID.String() != a.ID.String() || got.CreatedAt.IsZero() {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}

	all, err := s.ListChunks(ctx, &chunk.ListFilter{CollectionID: a.ID})
	if err != nil {
		t.Fatalf("list by collection: %v", err)
	}
	sameSet(t, "by collection", chunkIDs(all), append(chunkIDs(c1), chunkIDs(c2)...))
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		if prev.DocumentID.String() == cur.DocumentID.String() && prev.Index > cur.Index {
			t.Errorf("by collection: index out of order within %s", cur.DocumentID)
		}
		if prev.DocumentID.String() > cur.DocumentID.String() {
			t.Errorf("by collection: documents out of order at %d", i)
		}
	}

	page, err := s.ListChunks(ctx, &chunk.ListFilter{CollectionID: a.ID, Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	sameOrder(t, "page of two from offset two", chunkIDs(page), chunkIDs(all[2:4]))

	// An offset with no limit means "the rest", on every backend.
	rest, err := s.ListChunks(ctx, &chunk.ListFilter{CollectionID: a.ID, Offset: 2})
	if err != nil {
		t.Fatalf("list offset without limit: %v", err)
	}
	sameOrder(t, "offset two, no limit", chunkIDs(rest), chunkIDs(all[2:]))

	none, err := s.ListChunks(ctx, &chunk.ListFilter{CollectionID: a.ID, Tenant: ptr("t2")})
	if err != nil {
		t.Fatalf("list other tenant: %v", err)
	}
	sameSet(t, "other tenant", chunkIDs(none), nil)
}
