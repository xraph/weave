package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
)

// testTimestamps pins that created_at and updated_at survive every read path
// the dashboard uses: get and list, for collections and documents.
func testTimestamps(t *testing.T, s store.Store) {
	ctx := context.Background()

	col := mustCollection(t, s, "timestamps", "t1")
	gotCol, err := s.GetCollection(ctx, col.ID)
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	assertSameInstant(t, "GetCollection created_at", col.CreatedAt, gotCol.CreatedAt)
	assertSameInstant(t, "GetCollection updated_at", col.UpdatedAt, gotCol.UpdatedAt)

	cols, err := s.ListCollections(ctx, &collection.ListFilter{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(cols) != 1 {
		t.Fatalf("list collections: got %d rows, want 1", len(cols))
	}
	assertSameInstant(t, "ListCollections created_at", col.CreatedAt, cols[0].CreatedAt)

	doc := mustDocument(t, s, col, "stamped", document.StateProcessing)
	created := doc.CreatedAt

	gotDoc, err := s.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get document: %v", err)
	}
	assertSameInstant(t, "GetDocument created_at", created, gotDoc.CreatedAt)
	assertSameInstant(t, "GetDocument updated_at", doc.UpdatedAt, gotDoc.UpdatedAt)

	docs, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: col.ID})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("list documents: got %d rows, want 1", len(docs))
	}
	assertSameInstant(t, "ListDocuments created_at", created, docs[0].CreatedAt)

	// An update moves updated_at forward and leaves created_at alone.
	time.Sleep(5 * time.Millisecond)
	update := *gotDoc
	update.State = document.StateReady
	if err = s.UpdateDocument(ctx, &update); err != nil {
		t.Fatalf("update document: %v", err)
	}
	after, err := s.GetDocument(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get updated document: %v", err)
	}
	assertSameInstant(t, "created_at after update", created, after.CreatedAt)
	if !after.UpdatedAt.After(created) {
		t.Errorf("updated_at after update: got %v, want later than %v", after.UpdatedAt, created)
	}
}
