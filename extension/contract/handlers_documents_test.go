package contract

import (
	"context"
	"strings"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

func TestDocumentsIngestListGetSpansDelete(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		p := principal()
		col := mustCollection(t, deps, "kb")

		out, err := documentsIngestHandler(deps)(ctx, ingestInput{
			CollectionID: col.ID.String(), Title: "refunds", Source: "upload://refunds.md",
			SourceType: "text/markdown", Content: "refunds are issued within thirty days",
			Metadata: map[string]string{"lang": "en"},
		}, p)
		if err != nil || out.State != "ready" || out.ChunkCount < 1 || out.Error != "" || out.DocumentID == "" {
			t.Fatalf("ingest: %+v %v", out, err)
		}

		_, err = documentsIngestHandler(deps)(ctx, ingestInput{CollectionID: col.ID.String(), Content: "refunds are issued within thirty days"}, p)
		if codeOf(err) != dashcontract.CodeConflict {
			t.Errorf("duplicate: %v", err)
		}

		list, err := documentsListHandler(deps)(ctx, documentsListInput{CollectionID: col.ID.String(), State: "ready"}, p)
		if err != nil || list.Total != 1 || list.Items[0].CollectionName != "kb" || list.Items[0].Metadata["lang"] != "en" {
			t.Fatalf("list: %+v %v", list, err)
		}

		got, err := documentsGetHandler(deps)(ctx, idInput{ID: out.DocumentID}, p)
		if err != nil || got.Title != "refunds" || got.SourceType != "text/markdown" || got.Stalled {
			t.Fatalf("get: %+v %v", got, err)
		}

		spans, err := documentsSpansHandler(deps)(ctx, idInput{ID: out.DocumentID}, p)
		if err != nil || !spans.Complete || int(spans.Total) != len(spans.Spans) || spans.Spans[0].EndOffset == 0 || spans.ContentLength == 0 {
			t.Fatalf("spans: %+v %v", spans, err)
		}

		if _, err := documentsDeleteHandler(deps)(ctx, idInput{ID: out.DocumentID}, p); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := documentsGetHandler(deps)(ctx, idInput{ID: out.DocumentID}, p); codeOf(err) != dashcontract.CodeNotFound {
			t.Errorf("get after delete: %v", err)
		}
	})
}

// A failed ingest still wrote a document row, so it is an answer, not an
// error: the page shows the stored reason and links to the document.
func TestDocumentsIngest_FailureIsAnAnswer(t *testing.T) {
	deps := newDeps(t, openMemory(t), engine.WithEmbedder(failingEmbedder{}))
	ctx := context.Background()
	col := mustCollection(t, deps, "kb")
	out, err := documentsIngestHandler(deps)(ctx, ingestInput{CollectionID: col.ID.String(), Content: "anything at all"}, principal())
	if err != nil {
		t.Fatalf("ingest answered an error: %v", err)
	}
	if out.State != "failed" || !strings.Contains(out.Error, "quota exceeded") || out.DocumentID == "" {
		t.Errorf("failed ingest: %+v", out)
	}
}

func TestDocumentsIngest_Validation(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	col := mustCollection(t, deps, "kb")
	big := strings.Repeat("a", maxIngestBytes+1)
	cases := map[string]struct {
		in   ingestInput
		code dashcontract.ErrorCode
	}{
		"no collection":      {ingestInput{Content: "x"}, dashcontract.CodeBadRequest},
		"bad collection":     {ingestInput{CollectionID: "nope", Content: "x"}, dashcontract.CodeBadRequest},
		"missing collection": {ingestInput{CollectionID: id.NewCollectionID().String(), Content: "x"}, dashcontract.CodeNotFound},
		"blank content":      {ingestInput{CollectionID: col.ID.String(), Content: "  \n "}, dashcontract.CodeBadRequest},
		"over 1 MiB":         {ingestInput{CollectionID: col.ID.String(), Content: big}, dashcontract.CodeBadRequest},
	}
	for name, tc := range cases {
		if _, err := documentsIngestHandler(deps)(ctx, tc.in, principal()); codeOf(err) != tc.code {
			t.Errorf("%s: %v, want %s", name, err, tc.code)
		}
	}
}

func TestDocumentsList_Validation(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	if _, err := documentsListHandler(deps)(ctx, documentsListInput{State: "archived"}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("bad state: %v", err)
	}
	if _, err := documentsListHandler(deps)(ctx, documentsListInput{CollectionID: "nope"}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("bad collection: %v", err)
	}
	if _, err := documentsGetHandler(deps)(ctx, idInput{ID: id.NewDocumentID().String()}, principal()); codeOf(err) != dashcontract.CodeNotFound {
		t.Errorf("missing document: %v", err)
	}
	if _, err := documentsSpansHandler(deps)(ctx, idInput{ID: "nope"}, principal()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("bad spans id: %v", err)
	}
}
