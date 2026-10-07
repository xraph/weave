package contract

import (
	"context"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/id"
)

func TestCollectionsCreateListGet(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		p := principal()

		created, err := collectionsCreateHandler(deps)(ctx, collectionCreateInput{
			Name: "  Support  ", Description: "help centre", ChunkSize: 400, ChunkOverlap: 40,
			Metadata: map[string]string{"team": "cx"},
		}, p)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if created.Name != "Support" || created.ChunkSize != 400 || created.EmbeddingDims != 256 || created.Metadata["team"] != "cx" {
			t.Errorf("created: %+v", created.Collection)
		}
		mustIngest(t, ctx, deps, created.ID, "refunds", "refunds are issued within thirty days")

		list, err := collectionsListHandler(deps)(ctx, collectionsListInput{}, p)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if list.Total != 1 || len(list.Items) != 1 || list.Items[0].DocumentCount != 1 || list.Items[0].ChunkCount < 1 {
			t.Errorf("list: %+v", list)
		}
		if list.Limit != 25 || list.Offset != 0 {
			t.Errorf("paging echo: %+v", list)
		}

		got, err := collectionsGetHandler(deps)(ctx, idInput{ID: created.ID.String()}, p)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.DocumentsByState.Ready != 1 || got.DocumentCount != 1 || got.CreatedAt.IsZero() {
			t.Errorf("get: %+v", got)
		}

		if _, err := collectionsCreateHandler(deps)(ctx, collectionCreateInput{Name: "Support"}, p); codeOf(err) != dashcontract.CodeConflict {
			t.Errorf("duplicate name: %v", err)
		}
	})
}

func TestCollectionsCreate_Validation(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	for name, in := range map[string]collectionCreateInput{
		"blank name":           {Name: "   "},
		"negative size":        {Name: "a", ChunkSize: -1},
		"overlap at size":      {Name: "b", ChunkSize: 100, ChunkOverlap: 100},
		"overlap over default": {Name: "c", ChunkOverlap: 600},
	} {
		if _, err := collectionsCreateHandler(deps)(ctx, in, principal()); codeOf(err) != dashcontract.CodeBadRequest {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCollectionsGet_IDs(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	if _, err := collectionsGetHandler(deps)(ctx, idInput{ID: "garbage"}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("garbage: %v", err)
	}
	if _, err := collectionsGetHandler(deps)(ctx, idInput{ID: id.NewCollectionID().String()}, principal()); codeOf(err) != dashcontract.CodeNotFound {
		t.Errorf("missing: %v", err)
	}
}

func TestCollectionsList_TenantAndPaging(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	if err := deps.Engine.CreateCollection(weave.WithTenant(ctx, "t1"), &collection.Collection{Name: "tenanted"}); err != nil {
		t.Fatal(err)
	}
	mustCollection(t, deps, "open")

	empty := ""
	out, err := collectionsListHandler(deps)(ctx, collectionsListInput{Tenant: &empty}, principal())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if out.Total != 1 || out.Items[0].Name != "open" {
		t.Errorf("tenant \"\": %+v", out)
	}
	all, _ := collectionsListHandler(deps)(ctx, collectionsListInput{}, principal())
	if all.Total != 2 {
		t.Errorf("no tenant: total %d", all.Total)
	}
	if _, err := collectionsListHandler(deps)(ctx, collectionsListInput{page: page{Offset: -1}}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("negative offset: %v", err)
	}
}

func TestCollectionsUpdateDeleteReindex(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		p := principal()
		col := mustCollection(t, deps, "billing")
		mustIngest(t, ctx, deps, col.ID, "invoice", "invoices are sent monthly")

		name := "Billing"
		up, err := collectionsUpdateHandler(deps)(ctx, collectionUpdateInput{ID: col.ID.String(), Name: &name}, p)
		if err != nil || up.Name != "Billing" || up.DocumentCount != 1 {
			t.Fatalf("update: %+v %v", up, err)
		}

		re, err := collectionsReindexHandler(deps)(ctx, idInput{ID: col.ID.String()}, p)
		if err != nil || re.ReindexedDocuments != 1 || re.ID != col.ID.String() {
			t.Fatalf("reindex: %+v %v", re, err)
		}
		if _, err := collectionsReindexHandler(deps)(ctx, idInput{ID: id.NewCollectionID().String()}, p); codeOf(err) != dashcontract.CodeNotFound {
			t.Errorf("reindex missing: %v", err)
		}

		if _, err := collectionsDeleteHandler(deps)(ctx, idInput{ID: col.ID.String()}, p); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := collectionsDeleteHandler(deps)(ctx, idInput{ID: col.ID.String()}, p); codeOf(err) != dashcontract.CodeNotFound {
			t.Errorf("delete again: %v", err)
		}
	})
}
