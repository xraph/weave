package memory_test

import (
	"context"
	"testing"

	"github.com/xraph/weave/vectorstore"
	"github.com/xraph/weave/vectorstore/memory"
)

func TestSearchBreaksScoreTiesByID(t *testing.T) {
	ctx := context.Background()
	s := memory.New()
	vec := []float32{1, 0, 0}
	// Inserted out of ID order, all with the same vector.
	if err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "c", Vector: vec},
		{ID: "a", Vector: vec},
		{ID: "d", Vector: vec},
		{ID: "b", Vector: vec},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for run := range 50 {
		got, err := s.Search(ctx, vec, &vectorstore.SearchOptions{TopK: 2})
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
