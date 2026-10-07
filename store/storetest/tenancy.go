package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
)

func ptr(s string) *string { return &s }

// testTenancy pins what a tenant filter does on this backend, as a recorded
// fact: nil sees every tenant, and "" sees only rows written with no tenant.
// Every row Weave writes today has tenant "", so the second rule is the one
// that matters, and the obvious implementation ("skip the filter when the
// value is empty") gets it backwards.
func testTenancy(t *testing.T, s store.Store) {
	ctx := context.Background()

	c1 := mustCollection(t, s, "t1-col", "t1")
	c2 := mustCollection(t, s, "t2-col", "t2")
	c0 := mustCollection(t, s, "untenanted-col", "")
	d1 := mustDocument(t, s, c1, "t1 doc", document.StateReady)
	d2 := mustDocument(t, s, c2, "t2 doc", document.StateReady)
	d0 := mustDocument(t, s, c0, "untenanted doc", document.StateReady)
	ch1 := mustChunks(t, s, d1, 2)
	mustChunks(t, s, d2, 1)
	ch0 := mustChunks(t, s, d0, 3)

	cases := []struct {
		name   string
		tenant *string
		cols   []*collection.Collection
		docs   []*document.Document
		chunks int64
	}{
		{"nil sees everyone", nil, []*collection.Collection{c1, c2, c0}, []*document.Document{d1, d2, d0}, 6},
		{"t1 sees t1", ptr("t1"), []*collection.Collection{c1}, []*document.Document{d1}, int64(len(ch1))},
		{"empty sees only untenanted", ptr(""), []*collection.Collection{c0}, []*document.Document{d0}, int64(len(ch0))},
		{"unknown sees nothing", ptr("t9"), nil, nil, 0},
	}
	for _, tc := range cases {
		cols, err := s.ListCollections(ctx, &collection.ListFilter{Tenant: tc.tenant})
		if err != nil {
			t.Fatalf("%s: list collections: %v", tc.name, err)
		}
		sameSet(t, tc.name+": collections", collectionIDs(cols), collectionIDs(tc.cols))

		nCols, err := s.CountCollections(ctx, &collection.CountFilter{Tenant: tc.tenant})
		if err != nil {
			t.Fatalf("%s: count collections: %v", tc.name, err)
		}
		if nCols != int64(len(tc.cols)) {
			t.Errorf("%s: count collections: got %d, want %d", tc.name, nCols, len(tc.cols))
		}

		docs, err := s.ListDocuments(ctx, &document.ListFilter{Tenant: tc.tenant})
		if err != nil {
			t.Fatalf("%s: list documents: %v", tc.name, err)
		}
		sameSet(t, tc.name+": documents", documentIDs(docs), documentIDs(tc.docs))

		nDocs, err := s.CountDocuments(ctx, &document.CountFilter{Tenant: tc.tenant})
		if err != nil {
			t.Fatalf("%s: count documents: %v", tc.name, err)
		}
		if nDocs != int64(len(tc.docs)) {
			t.Errorf("%s: count documents: got %d, want %d", tc.name, nDocs, len(tc.docs))
		}

		nChunks, err := s.CountChunks(ctx, &chunk.CountFilter{Tenant: tc.tenant})
		if err != nil {
			t.Fatalf("%s: count chunks: %v", tc.name, err)
		}
		if nChunks != tc.chunks {
			t.Errorf("%s: count chunks: got %d, want %d", tc.name, nChunks, tc.chunks)
		}
	}
}

// testStalled pins UpdatedBefore, which the Overview uses to count
// documents stuck in processing.
func testStalled(t *testing.T, s store.Store) {
	ctx := context.Background()
	col := mustCollection(t, s, "stalled", "t1")
	mustDocument(t, s, col, "old", document.StateProcessing)
	time.Sleep(20 * time.Millisecond)
	cutoff := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	mustDocument(t, s, col, "fresh", document.StateProcessing)
	mustDocument(t, s, col, "done", document.StateReady)

	n, err := s.CountDocuments(ctx, &document.CountFilter{State: document.StateProcessing, UpdatedBefore: cutoff})
	if err != nil {
		t.Fatalf("count stalled: %v", err)
	}
	if n != 1 {
		t.Errorf("count stalled: got %d, want 1", n)
	}
}
