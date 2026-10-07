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

// serve registers the contract on fresh registries and returns a function
// that posts one envelope through forge's real HTTP transport.
func serve(t *testing.T, deps Deps) func(kind, intent, payload string) dashcontract.Response {
	t.Helper()
	reg := dashcontract.NewRegistry()
	wreg := dashcontract.NewWardenRegistry()
	d := dispatcher.New(nil)
	if err := Register(d, reg, wreg, deps); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := transport.NewHandler(reg, wreg, d, nil)
	n := 0
	return func(kind, intent, payload string) dashcontract.Response {
		n++
		body := `{"envelope":"v1","kind":"` + kind + `","contributor":"weave","intent":"` + intent + `",` +
			`"csrf":"test","idempotencyKey":"test-` + intent + `-` + strings.Repeat("x", n) + `","payload":` + payload + `}`
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/dashboard/v1", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
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
