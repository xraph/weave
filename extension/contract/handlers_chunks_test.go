package contract

import (
	"context"
	"strings"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/id"
)

func TestChunksListAndGet(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		p := principal()
		col := &collection.Collection{Name: "long", ChunkSize: 8, ChunkOverlap: 2}
		if err := deps.Engine.CreateCollection(ctx, col); err != nil {
			t.Fatal(err)
		}
		text := strings.Repeat("words that keep going ", 40)
		doc := mustIngest(t, ctx, deps, col.ID, "long", text)

		list, err := chunksListHandler(deps)(ctx, chunksListInput{DocumentID: doc.String(), page: page{Limit: 2, Offset: 1}}, p)
		if err != nil || len(list.Items) != 2 || list.Total < 3 || list.Items[0].Index != 1 {
			t.Fatalf("list: %+v %v", list, err)
		}
		for _, c := range list.Items {
			if c.Metadata == nil {
				t.Error("metadata marshals as null")
			}
		}

		mid := list.Items[0]
		got, err := chunksGetHandler(deps)(ctx, idInput{ID: mid.ID.String()}, p)
		if err != nil || got.Chunk.ID.String() != mid.ID.String() || got.DocumentTitle != "long" || got.PreviousID == "" || got.NextID == "" {
			t.Fatalf("get: %+v %v", got, err)
		}

		byCol, err := chunksListHandler(deps)(ctx, chunksListInput{CollectionID: col.ID.String()}, p)
		if err != nil || byCol.Total != list.Total {
			t.Errorf("by collection: %+v %v", byCol, err)
		}
	})
}

func TestChunks_Validation(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	if _, err := chunksListHandler(deps)(ctx, chunksListInput{}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("no scope: %v", err)
	}
	if _, err := chunksGetHandler(deps)(ctx, idInput{ID: "nope"}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("bad id: %v", err)
	}
	if _, err := chunksGetHandler(deps)(ctx, idInput{ID: id.NewChunkID().String()}, principal()); codeOf(err) != dashcontract.CodeNotFound {
		t.Errorf("missing: %v", err)
	}
}
