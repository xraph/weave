package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/transport"
)

// handler registers the contract on fresh registries and returns forge's
// real HTTP transport in front of it.
func handler(t *testing.T, deps Deps, opts ...transport.HandlerOption) http.Handler {
	t.Helper()
	reg := dashcontract.NewRegistry()
	wreg := dashcontract.NewWardenRegistry()
	d := dispatcher.New(nil)
	if err := Register(d, reg, wreg, deps); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return transport.NewHandler(reg, wreg, d, nil, opts...)
}

// postRaw posts one envelope to h and returns the recorded response.
func postRaw(h http.Handler, kind, intent, key, payload string) *httptest.ResponseRecorder {
	body := `{"envelope":"v1","kind":"` + kind + `","contributor":"weave","intent":"` + intent + `",` +
		`"csrf":"test","idempotencyKey":"` + key + `","payload":` + payload + `}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/dashboard/v1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// serve returns a function that posts one envelope through forge's real
// HTTP transport and decodes the answer.
func serve(t *testing.T, deps Deps) func(kind, intent, payload string) dashcontract.Response {
	t.Helper()
	h := handler(t, deps)
	n := 0
	return func(kind, intent, payload string) dashcontract.Response {
		n++
		rec := postRaw(h, kind, intent, "test-"+intent+"-"+strings.Repeat("x", n), payload)
		var resp dashcontract.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode %s: %v (%s)", intent, err, rec.Body)
		}
		return resp
	}
}

func TestCommandInvalidatesReachTheClient(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	post := serve(t, deps)

	created := post("command", "collections.create", `{"name":"kb"}`)
	if !created.OK {
		t.Fatalf("create failed: %s", created.Data)
	}
	want := map[string][]string{}
	for _, in := range loadManifest(t).Intents {
		want[in.Name] = in.Invalidates
	}
	if len(want["collections.create"]) == 0 || len(want["documents.ingest"]) == 0 {
		t.Fatal("manifest declares no invalidates for collections.create or documents.ingest; the assertions below would prove nothing")
	}
	if !reflect.DeepEqual(created.Meta.Invalidates, want["collections.create"]) {
		t.Fatalf("create invalidates = %v, want %v", created.Meta.Invalidates, want["collections.create"])
	}

	var row struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Data, &row); err != nil || row.ID == "" {
		t.Fatalf("created row: %s %v", created.Data, err)
	}
	ingested := post("command", "documents.ingest", `{"collection_id":"`+row.ID+`","title":"refunds","content":"refunds are issued within thirty days"}`)
	if !ingested.OK || !reflect.DeepEqual(ingested.Meta.Invalidates, want["documents.ingest"]) {
		t.Fatalf("ingest: ok=%v invalidates=%v (%s)", ingested.OK, ingested.Meta.Invalidates, ingested.Data)
	}

	listed := post("query", "documents.list", `{"collection_id":"`+row.ID+`"}`)
	if !listed.OK || !strings.Contains(string(listed.Data), `"collection_name":"kb"`) {
		t.Fatalf("list: %s", listed.Data)
	}
}

// Weave caps ingest content at 1 MiB, but forge's transport caps the whole
// envelope at 1 MiB first, and JSON escaping grows the content on the way.
// Content under Weave's cap can still be refused by the transport, with its
// own BAD_REQUEST. Raising the transport limit lets the same content in.
func TestIngest_TransportLimitShadowsTheContentCap(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	col := mustCollection(t, deps, "kb")

	// Each line is 18 bytes raw and 22 escaped (two quotes and a newline).
	content := strings.Repeat("he said \"refunds\"\n", 51200)
	payload, err := json.Marshal(ingestInput{CollectionID: col.ID.String(), Title: "quoted", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > maxIngestBytes || int64(len(payload)) <= transport.DefaultMaxBodyBytes {
		t.Fatalf("content %d bytes, payload %d bytes: want content under %d and payload over %d",
			len(content), len(payload), maxIngestBytes, transport.DefaultMaxBodyBytes)
	}

	rec := postRaw(handler(t, deps), "command", "documents.ingest", "big-default", string(payload))
	var refused dashcontract.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &refused); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	if rec.Code != http.StatusRequestEntityTooLarge || refused.OK || refused.Error == nil ||
		refused.Error.Code != dashcontract.CodeBadRequest || !strings.Contains(refused.Error.Message, "request body exceeds 1048576 bytes") {
		t.Fatalf("default limit: status %d, %+v", rec.Code, refused.Error)
	}

	rec = postRaw(handler(t, deps, transport.WithMaxBodyBytes(3<<20)), "command", "documents.ingest", "big-raised", string(payload))
	var ok dashcontract.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}
	var out ingestOutput
	if !ok.OK || json.Unmarshal(ok.Data, &out) != nil || out.State != "ready" {
		t.Fatalf("raised limit: status %d, %s", rec.Code, rec.Body.String()[:min(rec.Body.Len(), 300)])
	}
}
