package pipeline_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/pipeline"
	"github.com/xraph/weave/vectorstore"
)

func chunked(sc *pipeline.StepContext) *pipeline.StepContext {
	sc.Set(pipeline.KeyChunks, []chunker.ChunkResult{{Content: "refunds take thirty days", Index: 0}})
	return sc
}

func TestEmbedStepRefusesMissingTenant(t *testing.T) {
	for name, value := range map[string]any{"unset": nil, "empty": "", "not a string": 7} {
		emb := embedtest.New(8)
		sc := chunked(pipeline.NewStepContext())
		if value != nil {
			sc.Set(pipeline.KeyTenantID, value)
		}
		// A tenant on the Go context is not enough: the step reads its own.
		err := pipeline.NewEmbedStep(emb).Run(weave.WithTenant(context.Background(), "t1"), sc)
		if !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("%s: got %v, want ErrTenantRequired", name, err)
		}
		if n := emb.Calls(); n != 0 {
			t.Errorf("%s: embedder called %d times, want 0", name, n)
		}
		if _, ok := sc.Get(pipeline.KeyEntries); ok {
			t.Errorf("%s: entries were set", name)
		}
	}
}

func TestEmbedStepLabelsEntriesWithTheTenant(t *testing.T) {
	emb := embedtest.New(8)
	sc := chunked(pipeline.NewStepContext())
	sc.Set(pipeline.KeyTenantID, "t1")
	sc.Set(pipeline.KeyCollectionID, "col_1")
	if err := pipeline.NewEmbedStep(emb).Run(context.Background(), sc); err != nil {
		t.Fatalf("run: %v", err)
	}
	entries, ok := sc.MustGet(pipeline.KeyEntries).([]vectorstore.Entry)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries: %v", sc.MustGet(pipeline.KeyEntries))
	}
	if got := entries[0].Metadata["tenant_id"]; got != "t1" {
		t.Errorf("tenant_id: got %q, want t1", got)
	}
	if got := entries[0].Metadata["collection_id"]; got != "col_1" {
		t.Errorf("collection_id: got %q, want col_1", got)
	}
}
