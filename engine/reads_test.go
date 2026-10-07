package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/engine"
)

func TestRetrieveTenantFilter(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "tenants")
	d1 := mustIngest(t, weave.WithTenant(ctx, "t1"), r.Engine, col.ID, "t1 refunds", "refunds for tenant one")
	d0 := mustIngest(t, ctx, r.Engine, col.ID, "open refunds", "refunds for nobody in particular")
	mustIngest(t, weave.WithTenant(ctx, "t2"), r.Engine, col.ID, "t2 refunds", "refunds for tenant two")

	docsOf := func(hits []engine.ScoredChunk) map[string]bool {
		m := map[string]bool{}
		for _, h := range hits {
			m[h.Chunk.DocumentID.String()] = true
		}
		return m
	}

	t1, err := r.Engine.Retrieve(ctx, "refunds", engine.WithCollection(col.ID), engine.WithTenantFilter("t1"))
	if err != nil {
		t.Fatalf("t1: %v", err)
	}
	if got := docsOf(t1); len(got) != 1 || !got[d1.String()] {
		t.Errorf("t1 filter: got documents %v, want only %s", got, d1)
	}

	none, err := r.Engine.Retrieve(ctx, "refunds", engine.WithCollection(col.ID), engine.WithTenantFilter(""))
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if got := docsOf(none); len(got) != 1 || !got[d0.String()] {
		t.Errorf("empty tenant filter: got documents %v, want only the untenanted %s", got, d0)
	}

	all, err := r.Engine.Retrieve(ctx, "refunds", engine.WithCollection(col.ID))
	if err != nil {
		t.Fatalf("all: %v", err)
	}
	if got := docsOf(all); len(got) != 3 {
		t.Errorf("no filter: got %d documents, want 3", len(got))
	}
}

func TestListChunksNeedsAScope(t *testing.T) {
	e := newTestEngine(t)
	if _, err := e.ListChunks(context.Background(), &chunk.ListFilter{}); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("unscoped ListChunks: got %v, want ErrInvalidArgument", err)
	}
}

func TestListChunksPastTheEndIsAnEmptyArray(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	col := mustTestCollection(t, r.Engine, "past-the-end")
	mustIngest(t, ctx, r.Engine, col.ID, "refunds", "refunds are issued within thirty days")

	got, err := r.Engine.ListChunks(ctx, &chunk.ListFilter{CollectionID: col.ID, Offset: 10})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("list chunks past the end: got %#v, want a non-nil empty slice", got)
	}
}
