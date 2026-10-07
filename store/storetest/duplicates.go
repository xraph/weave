package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

func testDuplicates(t *testing.T, s store.Store) {
	ctx := context.Background()

	a := mustCollection(t, s, "dupes", "t1")
	b := mustCollection(t, s, "elsewhere", "t1")
	orig := mustDocument(t, s, a, "original", document.StateReady)

	again := &document.Document{
		ID: id.NewDocumentID(), CollectionID: a.ID, TenantID: "t1",
		Title: "again", ContentHash: orig.ContentHash, State: document.StatePending,
		Metadata: map[string]string{},
	}
	if err := s.CreateDocument(ctx, again); !errors.Is(err, weave.ErrDuplicateDocument) {
		t.Errorf("same hash, same collection: got %v, want ErrDuplicateDocument", err)
	}

	moved := *again
	moved.ID = id.NewDocumentID()
	moved.CollectionID = b.ID
	if err := s.CreateDocument(ctx, &moved); err != nil {
		t.Errorf("same hash, other collection: got %v, want success", err)
	}

	sameName := &collection.Collection{
		ID: id.NewCollectionID(), Name: "dupes", TenantID: "t1",
		EmbeddingModel: "m", ChunkStrategy: "recursive", ChunkSize: 64, ChunkOverlap: 8,
		Metadata: map[string]string{},
	}
	if err := s.CreateCollection(ctx, sameName); !errors.Is(err, weave.ErrCollectionAlreadyExists) {
		t.Errorf("same name, same tenant: got %v, want ErrCollectionAlreadyExists", err)
	}

	otherTenant := *sameName
	otherTenant.ID = id.NewCollectionID()
	otherTenant.TenantID = "t2"
	if err := s.CreateCollection(ctx, &otherTenant); err != nil {
		t.Errorf("same name, other tenant: got %v, want success", err)
	}

	// Renaming onto a taken name is refused too.
	rename, err := s.GetCollection(ctx, b.ID)
	if err != nil {
		t.Fatalf("get collection to rename: %v", err)
	}
	clone := *rename
	clone.Name = "dupes"
	if err := s.UpdateCollection(ctx, &clone); !errors.Is(err, weave.ErrCollectionAlreadyExists) {
		t.Errorf("rename onto taken name: got %v, want ErrCollectionAlreadyExists", err)
	}
	still, err := s.GetCollection(ctx, b.ID)
	if err != nil {
		t.Fatalf("get after refused rename: %v", err)
	}
	if still.Name != "elsewhere" {
		t.Errorf("refused rename changed the name to %q", still.Name)
	}
}
