package contract

import (
	"context"
	"testing"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

func TestSystemOverview(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		col := mustCollection(t, deps, "support")
		first := mustIngest(t, ctx, deps, col.ID, "refunds", "refunds are issued within thirty days")
		time.Sleep(5 * time.Millisecond)
		second := mustIngest(t, ctx, deps, col.ID, "shipping", "shipping takes five working days")

		// One document stuck in processing, last touched long ago as far
		// as the overview's clock is concerned.
		stuck := &document.Document{ID: id.NewDocumentID(), CollectionID: col.ID, Title: "stuck", ContentHash: "stuck", State: document.StateProcessing, Metadata: map[string]string{}}
		if err := deps.Engine.Store().CreateDocument(ctx, stuck); err != nil {
			t.Fatalf("seed stuck: %v", err)
		}
		deps.Now = func() time.Time { return time.Now().Add(StalledAfter + time.Minute) }

		out, err := systemOverviewHandler(deps)(ctx, tenantInput{}, principal())
		if err != nil {
			t.Fatalf("overview: %v", err)
		}
		if out.Collections != 1 || out.Documents != 3 || out.DocumentsByState.Ready != 2 || out.DocumentsByState.Processing != 1 {
			t.Errorf("counts: %+v", out)
		}
		if out.Chunks < 2 || out.Stalled != 1 || out.StalledAfterSeconds != int(StalledAfter.Seconds()) || out.Scope != "all" {
			t.Errorf("chunks/stalled/scope: %+v", out)
		}
		if len(out.NewestDocuments) != 3 || out.NewestDocuments[0].ID.String() != stuck.ID.String() ||
			out.NewestDocuments[1].ID.String() != second.String() || out.NewestDocuments[2].ID.String() != first.String() {
			t.Errorf("newest first: %+v", out.NewestDocuments)
		}
		if !out.NewestDocuments[0].Stalled || out.NewestDocuments[1].Stalled {
			t.Errorf("stalled flags: %+v", out.NewestDocuments)
		}
		if out.NewestDocuments[1].CollectionName != "support" {
			t.Errorf("collection name: %q", out.NewestDocuments[1].CollectionName)
		}
		if !out.Components.Embedder.Configured || out.Components.Retriever.Configured {
			t.Errorf("components: %+v", out.Components)
		}
	})
}

func TestSystemOverview_TenantFilter(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	col := mustCollection(t, deps, "mixed")
	mustIngest(t, weave.WithTenant(ctx, "t1"), deps, col.ID, "a", "alpha text")
	mustIngest(t, ctx, deps, col.ID, "b", "bravo text")

	empty := ""
	out, err := systemOverviewHandler(deps)(ctx, tenantInput{Tenant: &empty}, principal())
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if out.Documents != 1 || len(out.NewestDocuments) != 1 || out.NewestDocuments[0].Title != "b" {
		t.Errorf("tenant \"\": %+v", out)
	}
}

func TestSystemComponents(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	out, err := systemComponentsHandler(deps)(context.Background(), struct{}{}, principal())
	if err != nil {
		t.Fatalf("components: %v", err)
	}
	if out.Config.DefaultChunkSize != 512 || out.Config.DefaultTopK != 10 || out.Config.ShutdownTimeoutSeconds != 30 {
		t.Errorf("config: %+v", out.Config)
	}
	if out.Components.Chunker.Kind != "recursive" || out.Extensions == nil {
		t.Errorf("components %+v extensions %v", out.Components, out.Extensions)
	}
}
