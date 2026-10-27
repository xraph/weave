package contract

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dashauth "github.com/xraph/forge/extensions/dashboard/auth"
	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/embedtest"
)

// dispatchFunc sends one intent through forge's real dispatcher, so the
// tenant wrapper runs exactly as it does behind the dashboard.
type dispatchFunc func(ctx context.Context, intent, payload string, p dashcontract.Principal) (json.RawMessage, error)

// dispatchFor registers the contract for deps on a fresh dispatcher and
// returns a dispatchFunc that sends each intent with the kind the manifest
// declares for it.
func dispatchFor(t *testing.T, deps Deps) dispatchFunc {
	t.Helper()
	d := dispatcher.New(nil)
	if err := Register(d, dashcontract.NewRegistry(), dashcontract.NewWardenRegistry(), deps); err != nil {
		t.Fatalf("Register: %v", err)
	}
	kinds := map[string]dashcontract.Kind{}
	for _, in := range loadManifest(t).Intents {
		kinds[in.Name] = dashcontract.Kind(in.Kind)
	}
	return func(ctx context.Context, intent, payload string, p dashcontract.Principal) (json.RawMessage, error) {
		t.Helper()
		kind, ok := kinds[intent]
		if !ok {
			t.Fatalf("%s is not in the manifest", intent)
		}
		data, _, err := d.Dispatch(ctx, dashcontract.Request{
			Envelope: "v1", Kind: kind, Contributor: ContributorName, Intent: intent, IntentVersion: 1,
			Payload: json.RawMessage(payload),
		}, p)
		return data, err
	}
}

func as(subject string) dashcontract.Principal {
	return dashcontract.Principal{User: &dashauth.UserInfo{Subject: subject}}
}

// tenantsBySubject resolves a principal's tenant from a fixed table, the
// way a host maps its signed-in user to a workspace. An unknown user has
// none.
func tenantsBySubject(table map[string]string) func(context.Context, dashcontract.Principal) (string, error) {
	return func(_ context.Context, p dashcontract.Principal) (string, error) {
		if p.User == nil {
			return "", nil
		}
		return table[p.User.Subject], nil
	}
}

// seeded is one tenant's collection, document and first chunk.
type seeded struct {
	col   id.CollectionID
	doc   id.DocumentID
	chunk id.ChunkID
}

func seedTenant(t *testing.T, deps Deps, tenant, name, content string) seeded {
	t.Helper()
	ctx := weave.WithTenant(context.Background(), tenant)
	col := &collection.Collection{Name: name, ChunkSize: 512}
	if err := deps.Engine.CreateCollection(ctx, col); err != nil {
		t.Fatalf("create %s collection: %v", tenant, err)
	}
	doc := mustIngest(t, ctx, deps, col.ID, name, content)
	chunks, err := deps.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: doc})
	if err != nil || len(chunks) == 0 {
		t.Fatalf("list %s chunks: %d, %v", tenant, len(chunks), err)
	}
	return seeded{col: col.ID, doc: doc, chunk: chunks[0].ID}
}

// survives checks that tenant's rows are all still there.
func survives(t *testing.T, deps Deps, tenant string, s seeded) {
	t.Helper()
	ctx := weave.WithTenant(context.Background(), tenant)
	if _, err := deps.Engine.GetCollection(ctx, s.col); err != nil {
		t.Errorf("%s collection: %v", tenant, err)
	}
	if _, err := deps.Engine.GetDocument(ctx, s.doc); err != nil {
		t.Errorf("%s document: %v", tenant, err)
	}
	if _, err := deps.Engine.GetChunk(ctx, s.chunk); err != nil {
		t.Errorf("%s chunk: %v", tenant, err)
	}
}

func TestEveryIntentRefusesWithoutATenant(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		emb := embedtest.New(64)
		deps.Engine = newDeps(t, deps.Engine.Store(), engine.WithEmbedder(emb)).Engine
		deps.Tenant = nil
		theirs := seedTenant(t, deps, "t2", "theirs", "refunds are issued within thirty days")
		embeds := emb.Calls()
		call := dispatchFor(t, deps)

		byID := func(v string) string { return `{"id":"` + v + `"}` }
		payloads := map[string]string{
			"system.overview":     `{}`,
			"system.components":   `{}`,
			"collections.list":    `{}`,
			"collections.get":     byID(theirs.col.String()),
			"collections.create":  `{"name":"kb"}`,
			"collections.update":  `{"id":"` + theirs.col.String() + `","name":"renamed"}`,
			"collections.delete":  byID(theirs.col.String()),
			"collections.reindex": byID(theirs.col.String()),
			"documents.list":      `{}`,
			"documents.get":       byID(theirs.doc.String()),
			"documents.spans":     byID(theirs.doc.String()),
			"documents.ingest":    `{"collection_id":"` + theirs.col.String() + `","content":"more words"}`,
			"documents.delete":    byID(theirs.doc.String()),
			"chunks.list":         `{}`,
			"chunks.get":          byID(theirs.chunk.String()),
			"retrieval.run":       `{"query":"refunds"}`,
			"retrieval.assemble":  `{"hits":[]}`,
		}
		intents := loadManifest(t).Intents
		if len(intents) != 17 {
			t.Fatalf("manifest has %d intents, want 17", len(intents))
		}
		for _, in := range intents {
			payload, ok := payloads[in.Name]
			if !ok {
				t.Fatalf("no payload for %s", in.Name)
			}
			// Naming a tenant in the input changes nothing.
			for _, body := range []string{payload, strings.Replace(payload, "{", `{"tenant":"t2",`, 1)} {
				body = strings.Replace(body, `,}`, `}`, 1)
				data, err := call(context.Background(), in.Name, body, as("alice"))
				var ce *dashcontract.Error
				if !errors.As(err, &ce) || ce.Code != dashcontract.CodePermissionDenied {
					t.Errorf("%s %s: %v (%s), want PERMISSION_DENIED", in.Name, body, err, data)
				}
			}
		}

		t1 := weave.WithTenant(context.Background(), "t1")
		if n, err := deps.Engine.CountCollections(t1, &collection.CountFilter{}); err != nil || n != 0 {
			t.Errorf("t1 collections after a refused create: %d, %v", n, err)
		}
		t2 := weave.WithTenant(context.Background(), "t2")
		if n, err := deps.Engine.CountDocuments(t2, &document.CountFilter{}); err != nil || n != 1 {
			t.Errorf("t2 documents after a refused ingest: %d, %v", n, err)
		}
		if col, err := deps.Engine.GetCollection(t2, theirs.col); err != nil || col.Name != "theirs" {
			t.Errorf("t2 collection after a refused update: %+v, %v", col, err)
		}
		survives(t, deps, "t2", theirs)
		if got := emb.Calls(); got != embeds {
			t.Errorf("embedder called %d times by refused intents", got-embeds)
		}
	})
}

// A context tenant the host set some other way doesn't count when a
// resolver is configured: the resolver's answer for the principal wins.
func TestIntentsSeeOnlyTheResolvedTenant(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		deps.Tenant = tenantsBySubject(map[string]string{"alice": "t1"})
		ours := seedTenant(t, deps, "t1", "ours", "refunds are issued within thirty days")
		theirs := seedTenant(t, deps, "t2", "theirs", "shipping takes five working days")
		seedTenant(t, deps, "t2", "theirs too", "invoices are sent monthly")
		call := dispatchFor(t, deps)

		t1 := weave.WithTenant(context.Background(), "t1")
		wantChunks, err := deps.Engine.CountChunks(t1, &chunk.CountFilter{})
		if err != nil {
			t.Fatal(err)
		}

		for _, ctx := range []context.Context{context.Background(), weave.WithTenant(context.Background(), "t2")} {
			for _, c := range []struct {
				intent, payload string
				want            int64
			}{
				{"collections.list", `{}`, 1},
				{"documents.list", `{}`, 1},
				{"documents.list", `{"collection_id":"` + theirs.col.String() + `"}`, 0},
				{"chunks.list", `{"collection_id":"` + ours.col.String() + `"}`, wantChunks},
				{"chunks.list", `{"collection_id":"` + theirs.col.String() + `"}`, 0},
				{"chunks.list", `{"document_id":"` + theirs.doc.String() + `"}`, 0},
			} {
				intent, want := c.intent, c.want
				data, err := call(ctx, intent, c.payload, as("alice"))
				if err != nil {
					t.Fatalf("%s %s: %v", intent, c.payload, err)
				}
				var out struct {
					Items []json.RawMessage `json:"items"`
					Total int64             `json:"total"`
				}
				if err := json.Unmarshal(data, &out); err != nil {
					t.Fatalf("%s: %v (%s)", intent, err, data)
				}
				if out.Total != want || int64(len(out.Items)) != want {
					t.Errorf("%s %s: total %d, %d items, want %d of t1's", intent, c.payload, out.Total, len(out.Items), want)
				}
				if strings.Contains(string(data), `"t2"`) || strings.Contains(string(data), "theirs") {
					t.Errorf("%s shows t2's rows: %s", intent, data)
				}
			}

			data, err := call(ctx, "system.overview", `{}`, as("alice"))
			if err != nil {
				t.Fatalf("overview: %v", err)
			}
			var ov overviewOutput
			if err := json.Unmarshal(data, &ov); err != nil {
				t.Fatalf("overview: %v (%s)", err, data)
			}
			if ov.Collections != 1 || ov.Documents != 1 || ov.DocumentsByState.Ready != 1 || ov.Chunks != wantChunks || len(ov.NewestDocuments) != 1 {
				t.Errorf("overview counts t2's rows: %+v", ov)
			}
			if ov.Scope != "tenant" {
				t.Errorf("overview scope = %q, want tenant", ov.Scope)
			}
		}

		// Somebody the resolver doesn't know gets nothing.
		if _, err := call(context.Background(), "collections.list", `{}`, as("mallory")); codeOf(err) != dashcontract.CodePermissionDenied {
			t.Errorf("unknown user: %v, want PERMISSION_DENIED", err)
		}
	})
}

// Without a resolver the dashboard reads the tenant a host middleware put
// on the request context.
func TestNilResolverReadsTheContextTenant(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	deps.Tenant = nil
	seedTenant(t, deps, "t1", "ours", "refunds are issued within thirty days")
	seedTenant(t, deps, "t2", "theirs", "shipping takes five working days")
	call := dispatchFor(t, deps)

	data, err := call(weave.WithTenant(context.Background(), "t2"), "collections.list", `{}`, as("alice"))
	if err != nil {
		t.Fatalf("collections.list under t2: %v", err)
	}
	if !strings.Contains(string(data), `"theirs"`) || strings.Contains(string(data), `"ours"`) || !strings.Contains(string(data), `"total":1`) {
		t.Errorf("collections.list under t2: %s, want t2's one collection", data)
	}
	if _, err := call(weave.WithTenant(context.Background(), "t2"), "collections.list", `{"tenant":"t1"}`, as("alice")); codeOf(err) != dashcontract.CodePermissionDenied {
		t.Errorf("t2 naming t1: %v, want PERMISSION_DENIED", err)
	}
}

func TestTenantInputMustMatchTheResolvedTenant(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	deps.Tenant = tenantsBySubject(map[string]string{"alice": "t1"})
	ours := seedTenant(t, deps, "t1", "ours", "refunds are issued within thirty days")
	seedTenant(t, deps, "t2", "theirs", "refunds are issued within thirty days")
	call := dispatchFor(t, deps)

	for _, intent := range []string{"system.overview", "collections.list", "documents.list", "chunks.list", "retrieval.run"} {
		body := func(tenant string) string {
			var fields []string
			switch intent {
			case "retrieval.run":
				fields = append(fields, `"query":"refunds thirty days"`)
			case "chunks.list":
				fields = append(fields, `"collection_id":"`+ours.col.String()+`"`)
			}
			if tenant != "" {
				fields = append(fields, `"tenant":`+tenant)
			}
			return "{" + strings.Join(fields, ",") + "}"
		}
		for _, other := range []string{`"t2"`, `""`} {
			if data, err := call(context.Background(), intent, body(other), as("alice")); codeOf(err) != dashcontract.CodePermissionDenied {
				t.Errorf("%s naming %s: %v (%s), want PERMISSION_DENIED", intent, other, err, data)
			}
		}
		for _, same := range []string{`"t1"`, "null", ""} {
			if _, err := call(context.Background(), intent, body(same), as("alice")); err != nil {
				t.Errorf("%s %s: %v", intent, body(same), err)
			}
		}
	}
}

func TestByIDOfAnotherTenantIsNotFound(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		deps.Tenant = tenantsBySubject(map[string]string{"alice": "t1"})
		seedTenant(t, deps, "t1", "ours", "refunds are issued within thirty days")
		theirs := seedTenant(t, deps, "t2", "theirs", "shipping takes five working days")
		call := dispatchFor(t, deps)

		byID := func(v string) string { return `{"id":"` + v + `"}` }
		for _, c := range []struct{ intent, payload string }{
			{"collections.get", byID(theirs.col.String())},
			{"collections.update", `{"id":"` + theirs.col.String() + `","name":"renamed"}`},
			{"collections.reindex", byID(theirs.col.String())},
			{"documents.get", byID(theirs.doc.String())},
			{"documents.spans", byID(theirs.doc.String())},
			{"chunks.get", byID(theirs.chunk.String())},
			{"documents.delete", byID(theirs.doc.String())},
			{"collections.delete", byID(theirs.col.String())},
		} {
			if data, err := call(context.Background(), c.intent, c.payload, as("alice")); codeOf(err) != dashcontract.CodeNotFound {
				t.Errorf("%s of t2's row: %v (%s), want NOT_FOUND", c.intent, err, data)
			}
		}
		survives(t, deps, "t2", theirs)
		t2 := weave.WithTenant(context.Background(), "t2")
		if col, err := deps.Engine.GetCollection(t2, theirs.col); err != nil || col.Name != "theirs" {
			t.Errorf("t2 collection after a refused update: %+v, %v", col, err)
		}
	})
}

func TestResolverErrorIsPermissionDenied(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	deps.Tenant = func(context.Context, dashcontract.Principal) (string, error) {
		return "", errors.New("lookup failed for org_9")
	}
	_, err := dispatchFor(t, deps)(weave.WithTenant(context.Background(), "t1"), "collections.list", `{}`, as("alice"))
	var ce *dashcontract.Error
	if !errors.As(err, &ce) || ce.Code != dashcontract.CodePermissionDenied {
		t.Fatalf("resolver error: %v, want PERMISSION_DENIED", err)
	}
	if strings.Contains(ce.Message, "org_9") || strings.Contains(ce.Message, "lookup failed") {
		t.Errorf("the resolver's error reached the client: %q", ce.Message)
	}
}

// Dashboard ingest runs under the resolved tenant. A collection that
// belongs to another tenant is not found, and nothing lands in it.
func TestDocumentsIngestRunsUnderTheResolvedTenant(t *testing.T) {
	forEachStore(t, func(t *testing.T, deps Deps) {
		deps.Tenant = tenantsBySubject(map[string]string{"alice": "t1"})
		ours := mustCollection(t, deps, "ours")
		theirs := seedTenant(t, deps, "t2", "theirs", "shipping takes five working days")
		call := dispatchFor(t, deps)

		data, err := call(context.Background(), "documents.ingest",
			`{"collection_id":"`+ours.ID.String()+`","title":"refunds","content":"refunds are issued within thirty days"}`, as("alice"))
		if err != nil {
			t.Fatalf("ingest into t1's collection: %v", err)
		}
		var out ingestOutput
		if err = json.Unmarshal(data, &out); err != nil || out.State != string(document.StateReady) {
			t.Fatalf("ingest: %s, %v", data, err)
		}
		docID, err := id.ParseDocumentID(out.DocumentID)
		if err != nil {
			t.Fatal(err)
		}
		t1 := weave.WithTenant(context.Background(), "t1")
		doc, err := deps.Engine.GetDocument(t1, docID)
		if err != nil || doc.TenantID != "t1" {
			t.Fatalf("ingested document under t1: %+v, %v", doc, err)
		}
		chunks, err := deps.Engine.ListChunks(t1, &chunk.ListFilter{DocumentID: docID})
		if err != nil || len(chunks) == 0 {
			t.Fatalf("ingested chunks: %d, %v", len(chunks), err)
		}
		for _, c := range chunks {
			if c.TenantID != "t1" {
				t.Errorf("chunk %s tenant = %q, want t1", c.ID, c.TenantID)
			}
		}

		if data, err := call(context.Background(), "documents.ingest",
			`{"collection_id":"`+theirs.col.String()+`","title":"planted","content":"planted words for somebody else"}`, as("alice")); codeOf(err) != dashcontract.CodeNotFound {
			t.Errorf("ingest into t2's collection: %v (%s), want NOT_FOUND", err, data)
		}
		t2 := weave.WithTenant(context.Background(), "t2")
		if n, err := deps.Engine.CountDocuments(t2, &document.CountFilter{}); err != nil || n != 1 {
			t.Errorf("t2 documents after the refused ingest: %d, %v, want 1", n, err)
		}
		if n, err := deps.Engine.CountDocuments(t1, &document.CountFilter{CollectionID: theirs.col}); err != nil || n != 0 {
			t.Errorf("t1 documents in t2's collection: %d, %v, want 0", n, err)
		}
	})
}
