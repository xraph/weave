package contract

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
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

		// The wire shape: document fields sit at the top level of a row
		// next to the added ones, and metadata is an object, never null.
		raw, err := json.Marshal(out)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var wire map[string]any
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		rows, _ := wire["newest_documents"].([]any)
		if len(rows) == 0 {
			t.Fatalf("newest_documents on the wire: %v", wire["newest_documents"])
		}
		row, _ := rows[0].(map[string]any)
		for _, k := range []string{"collection_name", "stalled", "created_at", "updated_at"} {
			if _, ok := row[k]; !ok {
				t.Errorf("row is missing top-level key %q: %v", k, row)
			}
		}
		if _, ok := row["metadata"].(map[string]any); !ok {
			t.Errorf("metadata is not an object: %#v", row["metadata"])
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

// collectionLookupDown is a store whose collection lookups fail the way an
// outage does, which is not the same as a collection that is gone.
type collectionLookupDown struct{ store.Store }

func (collectionLookupDown) GetCollection(context.Context, id.CollectionID) (*collection.Collection, error) {
	return nil, errors.New("store down")
}

func TestSystemOverview_CollectionLookupFailure(t *testing.T) {
	ctx := context.Background()
	mem := memory.New()
	seed := newDeps(t, mem)
	col := mustCollection(t, seed, "support")
	mustIngest(t, ctx, seed, col.ID, "refunds", "refunds are issued within thirty days")

	deps := newDeps(t, collectionLookupDown{mem})
	_, err := systemOverviewHandler(deps)(ctx, tenantInput{}, principal())
	if err == nil {
		t.Fatal("overview answered despite a failed collection lookup")
	}
	if codeOf(err) != dashcontract.CodeInternal {
		t.Errorf("code = %q, want INTERNAL: %v", codeOf(err), err)
	}
}
