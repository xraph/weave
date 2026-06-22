package fabriqvec_test

import (
	"context"
	"testing"

	fabriqvec "github.com/xraph/weave/vectorstore/fabriq"

	"github.com/xraph/fabriq/core/registry"
	"github.com/xraph/fabriq/core/tenant"
	"github.com/xraph/fabriq/fabriqtest"
	"github.com/xraph/weave/vectorstore"
)

// tctx returns a context with the given tenant id set.
func tctx(t *testing.T, tenantID string) context.Context {
	t.Helper()
	ctx, err := tenant.WithTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("tenant.WithTenant(%q): %v", tenantID, err)
	}
	return ctx
}

func TestUpsertAndSearch(t *testing.T) {
	s := fabriqvec.New(fabriqtest.NewWorld(registry.New()).Vector)
	ctx := tctx(t, "acme")

	entries := []vectorstore.Entry{
		{ID: "a", Vector: []float32{1, 0, 0}, Content: "hello", Metadata: map[string]string{"tag": "x"}},
		{ID: "b", Vector: []float32{0, 1, 0}, Content: "world", Metadata: map[string]string{"tag": "y"}},
	}
	if err := s.Upsert(ctx, entries); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, &vectorstore.SearchOptions{TopK: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].ID != "a" {
		t.Errorf("expected top result id=a, got %q", results[0].ID)
	}
	if results[0].Content != "hello" {
		t.Errorf("expected content=hello, got %q", results[0].Content)
	}
	if results[0].Metadata["tag"] != "x" {
		t.Errorf("expected tag=x, got %q", results[0].Metadata["tag"])
	}
	// Vector is not returned from fabriq Similar.
	if results[0].Vector != nil {
		t.Errorf("expected Vector=nil, got %v", results[0].Vector)
	}
}

func TestSearchWithTenantKey(t *testing.T) {
	s := fabriqvec.New(fabriqtest.NewWorld(registry.New()).Vector)

	// Upsert under tenant "acme".
	if err := s.Upsert(tctx(t, "acme"), []vectorstore.Entry{
		{ID: "a1", Vector: []float32{1, 0, 0}, Content: "acme doc"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Search with TenantKey — should find the entry.
	results, err := s.Search(context.Background(), []float32{1, 0, 0}, &vectorstore.SearchOptions{
		TopK:      5,
		TenantKey: "acme",
	})
	if err != nil {
		t.Fatalf("Search with TenantKey: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected result for tenant acme")
	}
	if results[0].ID != "a1" {
		t.Errorf("expected id=a1, got %q", results[0].ID)
	}

	// Negative: a different TenantKey must return zero results — proving tenant isolation.
	other, err := s.Search(context.Background(), []float32{1, 0, 0}, &vectorstore.SearchOptions{
		TopK:      5,
		TenantKey: "other",
	})
	if err != nil {
		t.Fatalf("Search with TenantKey=other: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("expected 0 results for tenant 'other', got %d", len(other))
	}
}

func TestSearchMinScore(t *testing.T) {
	s := fabriqvec.New(fabriqtest.NewWorld(registry.New()).Vector)
	ctx := tctx(t, "tenant1")

	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "close", Vector: []float32{1, 0, 0}, Content: "close"},
		{ID: "far", Vector: []float32{0, 0, 1}, Content: "far"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Query close to "close"; "far" should be filtered by MinScore.
	results, err := s.Search(ctx, []float32{1, 0, 0}, &vectorstore.SearchOptions{
		TopK:     10,
		MinScore: 0.9,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range results {
		if r.Score < 0.9 {
			t.Errorf("result %q has score %f < MinScore 0.9", r.ID, r.Score)
		}
	}
}

func TestSearchFilter(t *testing.T) {
	s := fabriqvec.New(fabriqtest.NewWorld(registry.New()).Vector)
	ctx := tctx(t, "acme")

	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "x", Vector: []float32{1, 0, 0}, Content: "x doc", Metadata: map[string]string{"kind": "A"}},
		{ID: "y", Vector: []float32{1, 0, 0}, Content: "y doc", Metadata: map[string]string{"kind": "B"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, &vectorstore.SearchOptions{
		TopK:   10,
		Filter: map[string]string{"kind": "A"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].ID != "x" {
		t.Errorf("expected only result id=x, got %+v", results)
	}
}

func TestDelete(t *testing.T) {
	s := fabriqvec.New(fabriqtest.NewWorld(registry.New()).Vector)
	ctx := tctx(t, "acme")

	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "del1", Vector: []float32{1, 0, 0}, Content: "to delete"},
		{ID: "keep", Vector: []float32{0, 1, 0}, Content: "to keep"},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := s.Delete(ctx, []string{"del1"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, &vectorstore.SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("Search after delete: %v", err)
	}
	for _, r := range results {
		if r.ID == "del1" {
			t.Error("deleted entry del1 still appears in search results")
		}
	}
}

func TestDeleteByMetadata(t *testing.T) {
	s := fabriqvec.New(fabriqtest.NewWorld(registry.New()).Vector)
	ctx := tctx(t, "acme")

	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "m1", Vector: []float32{1, 0, 0}, Content: "meta1", Metadata: map[string]string{"group": "old"}},
		{ID: "m2", Vector: []float32{0, 1, 0}, Content: "meta2", Metadata: map[string]string{"group": "new"}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if err := s.DeleteByMetadata(ctx, map[string]string{"group": "old"}); err != nil {
		t.Fatalf("DeleteByMetadata: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, &vectorstore.SearchOptions{TopK: 10})
	if err != nil {
		t.Fatalf("Search after DeleteByMetadata: %v", err)
	}
	for _, r := range results {
		if r.ID == "m1" {
			t.Error("entry m1 (group=old) should have been deleted by DeleteByMetadata")
		}
	}
	found := false
	for _, r := range results {
		if r.ID == "m2" {
			found = true
		}
	}
	if !found {
		t.Error("entry m2 (group=new) should still exist after DeleteByMetadata")
	}
}

func TestWithEntity(t *testing.T) {
	world := fabriqtest.NewWorld(registry.New())
	s := fabriqvec.New(world.Vector, fabriqvec.WithEntity("custom_entity"))
	ctx := tctx(t, "acme")

	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "e1", Vector: []float32{1, 0, 0}, Content: "custom"},
	}); err != nil {
		t.Fatalf("Upsert with custom entity: %v", err)
	}

	results, err := s.Search(ctx, []float32{1, 0, 0}, &vectorstore.SearchOptions{TopK: 5})
	if err != nil {
		t.Fatalf("Search with custom entity: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected result with custom entity")
	}
	if results[0].ID != "e1" {
		t.Errorf("expected id=e1, got %q", results[0].ID)
	}
}
