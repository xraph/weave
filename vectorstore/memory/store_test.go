package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/vectorstore"
	"github.com/xraph/weave/vectorstore/memory"
)

func owned(tenant string) map[string]string { return map[string]string{"tenant_id": tenant} }

func TestSearchBreaksScoreTiesByID(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	vec := []float32{1, 0, 0}
	// Inserted out of ID order, all with the same vector.
	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "c", Vector: vec, Metadata: owned("t1")},
		{ID: "a", Vector: vec, Metadata: owned("t1")},
		{ID: "d", Vector: vec, Metadata: owned("t1")},
		{ID: "b", Vector: vec, Metadata: owned("t1")},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for run := range 50 {
		got, err := s.Search(ctx, vec, &vectorstore.SearchOptions{TopK: 2, TenantKey: "t1"})
		if err != nil {
			t.Fatalf("run %d: search: %v", run, err)
		}
		if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
			ids := make([]string, len(got))
			for i, r := range got {
				ids[i] = r.ID
			}
			t.Fatalf("run %d: got %v, want [a b] for tied scores", run, ids)
		}
	}
}

func TestSearchRefusesEmptyTenantKey(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	vec := []float32{1, 0, 0}
	if err := s.Upsert(ctx, []vectorstore.Entry{{ID: "a", Vector: vec, Metadata: owned("t1")}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for name, opts := range map[string]*vectorstore.SearchOptions{
		"nil options":           nil,
		"no tenant key":         {TopK: 5},
		"tenant only in filter": {TopK: 5, Filter: owned("t1")},
	} {
		got, err := s.Search(ctx, vec, opts)
		if !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("%s: got %v, %d results; want ErrTenantRequired", name, err, len(got))
		}
	}
	got, err := s.Search(ctx, vec, &vectorstore.SearchOptions{TopK: 5, TenantKey: "t2"})
	if err != nil || len(got) != 0 {
		t.Errorf("t2 search: %d results, %v; want none", len(got), err)
	}
}

func TestUpsertRefusesEntryWithoutTenant(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	vec := []float32{1, 0, 0}
	if err := s.Upsert(ctx, []vectorstore.Entry{{ID: "a", Vector: vec, Metadata: owned("t1")}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for name, batch := range map[string][]vectorstore.Entry{
		"no metadata":     {{ID: "b", Vector: vec}},
		"empty tenant":    {{ID: "b", Vector: vec, Metadata: owned("")}},
		"one of two":      {{ID: "b", Vector: vec, Metadata: owned("t1")}, {ID: "c", Vector: vec}},
		"overwrite a bad": {{ID: "a", Vector: vec, Metadata: map[string]string{"kind": "x"}}},
	} {
		if err := s.Upsert(ctx, batch); !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("%s: got %v, want ErrTenantRequired", name, err)
		}
		if n := s.Len(); n != 1 {
			t.Errorf("%s: Len() = %d after a refused upsert, want 1", name, n)
		}
	}
	got, err := s.Search(ctx, vec, &vectorstore.SearchOptions{TopK: 5, TenantKey: "t1"})
	if err != nil || len(got) != 1 || got[0].Metadata["tenant_id"] != "t1" {
		t.Errorf("t1 entry after refused upserts: %+v, %v", got, err)
	}
}

func TestDeleteByMetadataRefusesEmptyFilter(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	vec := []float32{1, 0, 0}
	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "a", Vector: vec, Metadata: owned("t1")},
		{ID: "b", Vector: vec, Metadata: owned("t2")},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for name, filter := range map[string]map[string]string{"nil": nil, "empty": {}} {
		if err := s.DeleteByMetadata(ctx, filter); !errors.Is(err, weave.ErrInvalidArgument) {
			t.Errorf("%s filter: got %v, want ErrInvalidArgument", name, err)
		}
		if n := s.Len(); n != 2 {
			t.Errorf("%s filter: Len() = %d, want both entries kept", name, n)
		}
	}
	if err := s.DeleteByMetadata(ctx, owned("t2")); err != nil {
		t.Fatalf("delete t2: %v", err)
	}
	if n := s.Len(); n != 1 {
		t.Errorf("Len() = %d after deleting t2's entry, want 1", n)
	}
}
