package contract

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xraph/forge"
	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	forgetesting "github.com/xraph/forge/testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
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
		t.Fatalf("failed ingest: %+v", out)
	}
	docID, err := id.ParseDocumentID(out.DocumentID)
	if err != nil {
		t.Fatalf("document id: %v", err)
	}
	doc, err := deps.Engine.GetDocument(ctx, docID)
	if err != nil {
		t.Fatalf("re-read the failed document: %v", err)
	}
	if doc.State != document.StateFailed || doc.Error != out.Error {
		t.Errorf("stored state %q error %q, answer said error %q", doc.State, doc.Error, out.Error)
	}
}

// documentReadDown fails every document read, the way an outage does.
type documentReadDown struct{ store.Store }

func (documentReadDown) GetDocument(context.Context, id.DocumentID) (*document.Document, error) {
	return nil, errors.New("store down")
}

// When the failed document cannot be read back, the answer still reports
// the failure with the error Ingest returned, and the read error is logged.
func TestDocumentsIngest_FailureWithUnreadableDocument(t *testing.T) {
	for name, logger := range map[string]forge.Logger{"no logger": nil, "logger": forgetesting.NewTestLogger()} {
		t.Run(name, func(t *testing.T) {
			deps := newDeps(t, documentReadDown{memory.New()}, engine.WithEmbedder(failingEmbedder{}))
			deps.Logger = logger
			ctx := context.Background()
			col := mustCollection(t, deps, "kb")
			out, err := documentsIngestHandler(deps)(ctx, ingestInput{CollectionID: col.ID.String(), Content: "anything at all"}, principal())
			if err != nil {
				t.Fatalf("ingest answered an error: %v", err)
			}
			if out.State != "failed" || !strings.Contains(out.Error, "quota exceeded") {
				t.Errorf("failed ingest: %+v", out)
			}
		})
	}
}

// A dashboard request carries no tenant. Ingest into a tenant's collection
// must still land the document, its chunks and its vectors in that tenant.
func TestDocumentsIngest_UsesTheCollectionsTenant(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		ctx := context.Background()
		p := principal()
		col := &collection.Collection{Name: "kb", ChunkSize: 512}
		if err := deps.Engine.CreateCollection(weave.WithApp(weave.WithTenant(ctx, "acme"), "portal"), col); err != nil {
			t.Fatal(err)
		}
		out, err := documentsIngestHandler(deps)(ctx, ingestInput{CollectionID: col.ID.String(), Title: "refunds", Content: "refunds are issued within thirty days"}, p)
		if err != nil || out.State != "ready" {
			t.Fatalf("ingest: %+v %v", out, err)
		}

		docID, err := id.ParseDocumentID(out.DocumentID)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := deps.Engine.GetDocument(ctx, docID)
		if err != nil || doc.TenantID != "acme" {
			t.Fatalf("document tenant: %+v %v", doc, err)
		}
		chunks, err := deps.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: docID})
		if err != nil || len(chunks) == 0 {
			t.Fatalf("chunks: %v %v", chunks, err)
		}
		for _, c := range chunks {
			if c.TenantID != "acme" {
				t.Errorf("chunk %s tenant %q, want acme", c.ID, c.TenantID)
			}
		}

		acme, untenanted := "acme", ""
		list, err := documentsListHandler(deps)(ctx, documentsListInput{Tenant: &acme}, p)
		if err != nil || list.Total != 1 || list.Items[0].ID.String() != out.DocumentID {
			t.Errorf("documents.list tenant acme: %+v %v", list, err)
		}
		list, err = documentsListHandler(deps)(ctx, documentsListInput{Tenant: &untenanted}, p)
		if err != nil || list.Total != 0 {
			t.Errorf("documents.list tenant \"\": %+v %v", list, err)
		}

		// The vectors carry the tenant too: retrieval filtered to acme finds
		// the document, and filtered to "" finds nothing.
		run, err := retrievalRunHandler(deps)(ctx, runInput{Query: "refunds", Tenant: &acme}, p)
		if err != nil || len(run.Result.Hits) == 0 || run.Result.Hits[0].Chunk.DocumentID.String() != out.DocumentID {
			t.Errorf("retrieval tenant acme: %+v %v", run.Result, err)
		}
		run, err = retrievalRunHandler(deps)(ctx, runInput{Query: "refunds", Tenant: &untenanted}, p)
		if err != nil || len(run.Result.Hits) != 0 {
			t.Errorf("retrieval tenant \"\": %+v %v", run.Result, err)
		}
	})
}

// cancellingEmbedder cancels the request on its first call, the way a
// closed tab or a proxy timeout does partway through ingest or reindex,
// and then behaves like a real client: a done context fails the call.
type cancellingEmbedder struct {
	hashEmbedder
	cancel context.CancelFunc
}

func (c cancellingEmbedder) Embed(ctx context.Context, texts []string) ([]embedder.EmbedResult, error) {
	c.cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.hashEmbedder.Embed(ctx, texts)
}

// Cancelling the request partway must not leave the document half done.
func TestDocumentsIngest_SurvivesCancellation(t *testing.T) {
	for _, b := range []struct {
		name string
		open func(*testing.T) store.Store
	}{{"memory", openMemory}, {"sqlite", openSQLite}} {
		t.Run(b.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			deps := newDeps(t, b.open(t), engine.WithEmbedder(cancellingEmbedder{hashEmbedder: hashEmbedder{dims: 256}, cancel: cancel}))
			col := mustCollection(t, deps, "kb")

			out, err := documentsIngestHandler(deps)(ctx, ingestInput{CollectionID: col.ID.String(), Content: "refunds are issued within thirty days"}, principal())
			if ctx.Err() == nil {
				t.Fatal("the request was never cancelled; the test proves nothing")
			}
			if err != nil || out.State != "ready" || out.ChunkCount < 1 {
				t.Fatalf("ingest: %+v %v", out, err)
			}
			docID, _ := id.ParseDocumentID(out.DocumentID)
			doc, err := deps.Engine.GetDocument(context.Background(), docID)
			if err != nil || doc.State != document.StateReady {
				t.Errorf("stored document: %+v %v, want ready", doc, err)
			}
		})
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
