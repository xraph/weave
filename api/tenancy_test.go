package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xraph/forge"

	"github.com/xraph/weave"
	"github.com/xraph/weave/api"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/store/memory"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(
		engine.WithStore(memory.New()),
		engine.WithVectorStore(vsmemory.New()),
		engine.WithEmbedder(embedtest.New(64)),
		engine.WithChunker(chunker.NewRecursiveChunker()),
	)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return e
}

// seed creates a collection holding one document under tenant and returns
// both IDs.
func seed(t *testing.T, e *engine.Engine, tenant, name string) (id.CollectionID, id.DocumentID) {
	t.Helper()
	ctx := weave.WithTenant(context.Background(), tenant)
	col := &collection.Collection{Name: name, ChunkSize: 512}
	if err := e.CreateCollection(ctx, col); err != nil {
		t.Fatalf("create collection: %v", err)
	}
	res, err := e.Ingest(ctx, &engine.IngestInput{CollectionID: col.ID, Content: "refunds are issued within thirty days"})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	return col.ID, res.DocumentID
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequestWithContext(t.Context(), method, path, nil)
	} else {
		req = httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRoutesAnswerForbiddenWithoutTenant(t *testing.T) {
	e := newEngine(t)
	col, doc := seed(t, e, "t1", "docs")
	h := api.New(e, nil).Handler()

	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/v1/collections", ""},
		{http.MethodPost, "/v1/collections", `{"name":"new"}`},
		{http.MethodGet, "/v1/collections/" + col.String(), ""},
		{http.MethodGet, "/v1/collections/" + col.String() + "/stats", ""},
		{http.MethodDelete, "/v1/collections/" + col.String(), ""},
		{http.MethodPost, "/v1/collections/" + col.String() + "/reindex", ""},
		{http.MethodPost, "/v1/collections/" + col.String() + "/documents", `{"content":"more words"}`},
		{http.MethodPost, "/v1/collections/" + col.String() + "/documents/batch", `{"documents":[{"content":"more words"}]}`},
		{http.MethodGet, "/v1/collections/" + col.String() + "/documents", ""},
		{http.MethodGet, "/v1/documents/" + doc.String(), ""},
		{http.MethodDelete, "/v1/documents/" + doc.String(), ""},
		{http.MethodPost, "/v1/collections/" + col.String() + "/retrieve", `{"query":"refunds"}`},
		{http.MethodPost, "/v1/search", `{"query":"refunds"}`},
		// Malformed requests are refused for the missing tenant first.
		{http.MethodGet, "/v1/collections/nope", ""},
		{http.MethodPost, "/v1/collections/" + col.String() + "/retrieve", `{"query":""}`},
		{http.MethodPost, "/v1/collections/" + col.String() + "/documents", `{"content":""}`},
		{http.MethodPost, "/v1/collections/nope/documents/batch", `{"documents":[{"content":""}]}`},
	}
	for _, rt := range routes {
		rec := do(t, h, rt.method, rt.path, rt.body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "weave: this request has no tenant") {
			t.Errorf("%s %s: status %d (%s), want 403 saying there is no tenant", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}

	// Nothing was changed by the refused calls.
	ctx := weave.WithTenant(context.Background(), "t1")
	if _, err := e.GetDocument(ctx, doc); err != nil {
		t.Errorf("t1 document after refused calls: %v", err)
	}
	if n, err := e.CountCollections(ctx, nil); err != nil || n != 1 {
		t.Errorf("t1 collections after refused calls: %d, %v; want 1", n, err)
	}
}

func TestRoutesUseTheRequestTenant(t *testing.T) {
	e := newEngine(t)
	mine, _ := seed(t, e, "t1", "mine")
	theirCol, theirDoc := seed(t, e, "t2", "theirs")

	router := forge.NewRouter()
	// Stands in for the host's auth middleware, which sets the tenant from
	// the authenticated principal.
	router.Use(func(next forge.Handler) forge.Handler {
		return func(c forge.Context) error {
			c.WithContext(weave.WithTenant(c.Context(), "t1"))
			return next(c)
		}
	})
	h := api.New(e, router).Handler()

	rec := do(t, h, http.MethodGet, "/v1/collections", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list collections: status %d (%s)", rec.Code, rec.Body.String())
	}
	var cols []collection.Collection
	if err := json.Unmarshal(rec.Body.Bytes(), &cols); err != nil {
		t.Fatalf("decode collections: %v (%s)", err, rec.Body.String())
	}
	if len(cols) != 1 || cols[0].ID.String() != mine.String() || cols[0].TenantID != "t1" {
		t.Errorf("list collections: got %+v, want only t1's", cols)
	}

	if got := do(t, h, http.MethodGet, "/v1/documents/"+theirDoc.String(), ""); got.Code != http.StatusNotFound {
		t.Errorf("get t2's document: status %d (%s), want 404", got.Code, got.Body.String())
	}
	if got := do(t, h, http.MethodDelete, "/v1/documents/"+theirDoc.String(), ""); got.Code != http.StatusNotFound {
		t.Errorf("delete t2's document: status %d (%s), want 404", got.Code, got.Body.String())
	}
	for _, rt := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/collections/" + theirCol.String(), ""},
		{http.MethodGet, "/v1/collections/" + theirCol.String() + "/stats", ""},
		{http.MethodPost, "/v1/collections/" + theirCol.String() + "/reindex", ""},
		{http.MethodDelete, "/v1/collections/" + theirCol.String(), ""},
		{http.MethodPost, "/v1/collections/" + theirCol.String() + "/documents", `{"content":"more words"}`},
		{http.MethodPost, "/v1/collections/" + theirCol.String() + "/documents/batch", `{"documents":[{"content":"more words"}]}`},
		{http.MethodGet, "/v1/collections/" + theirCol.String() + "/documents", ""},
		{http.MethodPost, "/v1/collections/" + theirCol.String() + "/retrieve", `{"query":"refunds","top_k":5}`},
		{http.MethodPost, "/v1/search", `{"query":"refunds","collections":["` + theirCol.String() + `"]}`},
	} {
		if got := do(t, h, rt.method, rt.path, rt.body); got.Code != http.StatusNotFound {
			t.Errorf("%s %s: status %d (%s), want 404", rt.method, rt.path, got.Code, got.Body.String())
		}
	}
	if _, err := e.GetDocument(weave.WithTenant(context.Background(), "t2"), theirDoc); err != nil {
		t.Errorf("t2 document after t1's delete: %v", err)
	}

	rec = do(t, h, http.MethodPost, "/v1/search", `{"query":"refunds thirty days","top_k":10}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status %d (%s)", rec.Code, rec.Body.String())
	}
	var hits []engine.ScoredChunk
	if err := json.Unmarshal(rec.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode hits: %v", err)
	}
	if len(hits) != 1 || hits[0].Chunk.TenantID != "t1" {
		t.Errorf("search: got %d hits (%s), want t1's one", len(hits), rec.Body.String())
	}

	rec = do(t, h, http.MethodPost, "/v1/collections", `{"name":"other","metadata":{}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create collection: status %d (%s)", rec.Code, rec.Body.String())
	}
	var created collection.Collection
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.TenantID != "t1" {
		t.Errorf("created collection: tenant %q, %v; want t1", created.TenantID, err)
	}
}

func TestEmptyResultsAreArrays(t *testing.T) {
	e := newEngine(t)
	col, _ := seed(t, e, "t1", "docs")
	router := forge.NewRouter()
	router.Use(func(next forge.Handler) forge.Handler {
		return func(c forge.Context) error {
			c.WithContext(weave.WithTenant(c.Context(), "t1"))
			return next(c)
		}
	})
	h := api.New(e, router).Handler()

	for _, rt := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/collections?offset=10", ""},
		{http.MethodGet, "/v1/collections/" + col.String() + "/documents?state=failed", ""},
		{http.MethodPost, "/v1/search", `{"query":"nothing matches","collections":["` + col.String() + `"],"min_score":0.99}`},
		{http.MethodPost, "/v1/collections/" + col.String() + "/retrieve", `{"query":"nothing matches","top_k":5,"min_score":0.99}`},
	} {
		rec := do(t, h, rt.method, rt.path, rt.body)
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
			t.Errorf("%s %s: status %d, body %q; want 200 []", rt.method, rt.path, rec.Code, rec.Body.String())
		}
	}
}
