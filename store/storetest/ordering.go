package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
)

// testOrdering pins default ascending order and SortDesc, with paging. The
// sleeps keep created_at distinct even on Mongo's millisecond clock.
func testOrdering(t *testing.T, s store.Store) {
	ctx := context.Background()

	var cols []*collection.Collection
	for _, name := range []string{"first", "second", "third"} {
		cols = append(cols, mustCollection(t, s, name, "t1"))
		time.Sleep(5 * time.Millisecond)
	}
	var docs []*document.Document
	for _, title := range []string{"one", "two", "three"} {
		docs = append(docs, mustDocument(t, s, cols[0], title, document.StateReady))
		time.Sleep(5 * time.Millisecond)
	}

	asc, err := s.ListCollections(ctx, &collection.ListFilter{})
	if err != nil {
		t.Fatalf("list collections asc: %v", err)
	}
	sameOrder(t, "collections default", collectionIDs(asc), collectionIDs(cols))

	desc, err := s.ListCollections(ctx, &collection.ListFilter{SortDesc: true})
	if err != nil {
		t.Fatalf("list collections desc: %v", err)
	}
	restCols, err := s.ListCollections(ctx, &collection.ListFilter{Offset: 1})
	if err != nil {
		t.Fatalf("list collections offset without limit: %v", err)
	}
	sameOrder(t, "collections offset one, no limit", collectionIDs(restCols), collectionIDs(cols[1:]))

	sameOrder(t, "collections newest first", collectionIDs(desc), collectionIDs([]*collection.Collection{cols[2], cols[1], cols[0]}))

	page, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: cols[0].ID, SortDesc: true, Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("list documents desc page: %v", err)
	}
	sameOrder(t, "documents newest first, second page of two", documentIDs(page), documentIDs([]*document.Document{docs[1], docs[0]}))

	ascDocs, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: cols[0].ID})
	if err != nil {
		t.Fatalf("list documents asc: %v", err)
	}
	sameOrder(t, "documents default", documentIDs(ascDocs), documentIDs(docs))

	restDocs, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: cols[0].ID, SortDesc: false, Offset: 1})
	if err != nil {
		t.Fatalf("list documents offset without limit: %v", err)
	}
	sameOrder(t, "documents offset one, no limit", documentIDs(restDocs), documentIDs(docs[1:]))
}
