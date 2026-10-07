package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/engine"
)

func TestUpdateCollection(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	col := mustTestCollection(t, e, "support")
	col.Description = "help centre"
	col.Metadata = map[string]string{"team": "cx"}
	if _, err := e.UpdateCollection(ctx, col.ID, engine.CollectionUpdate{Description: &col.Description, Metadata: &col.Metadata}); err != nil {
		t.Fatalf("seed update: %v", err)
	}
	beforeSize := col.ChunkSize

	name := "customer support"
	got, err := e.UpdateCollection(ctx, col.ID, engine.CollectionUpdate{Name: &name})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.Name != name || got.Description != "help centre" || got.Metadata["team"] != "cx" {
		t.Errorf("rename touched other fields: %+v", got)
	}
	if got.ChunkSize != beforeSize {
		t.Errorf("rename changed chunk size to %d", got.ChunkSize)
	}

	empty := map[string]string{}
	got, err = e.UpdateCollection(ctx, col.ID, engine.CollectionUpdate{Metadata: &empty})
	if err != nil {
		t.Fatalf("clear metadata: %v", err)
	}
	if len(got.Metadata) != 0 {
		t.Errorf("clear metadata: still %v", got.Metadata)
	}

	blank := "   "
	if _, err := e.UpdateCollection(ctx, col.ID, engine.CollectionUpdate{Name: &blank}); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("blank name: got %v, want ErrInvalidArgument", err)
	}
	stored, err := e.GetCollection(ctx, col.ID)
	if err != nil {
		t.Fatalf("get after refused update: %v", err)
	}
	if stored.Name != name {
		t.Errorf("refused update changed the name to %q", stored.Name)
	}

	mustTestCollection(t, e, "billing")
	taken := "billing"
	if _, err := e.UpdateCollection(ctx, col.ID, engine.CollectionUpdate{Name: &taken}); !errors.Is(err, weave.ErrCollectionAlreadyExists) {
		t.Errorf("taken name: got %v, want ErrCollectionAlreadyExists", err)
	}
}
