package contract

import (
	"bytes"
	"reflect"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/loader"
)

func loadManifest(t *testing.T) *dashcontract.ContractManifest {
	t.Helper()
	m, err := loader.Load(bytes.NewReader(manifestYAML), "weave/contract/manifest.yaml")
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return m
}

func TestManifest_Loads(t *testing.T) {
	m := loadManifest(t)
	if m.Contributor.Name != ContributorName {
		t.Errorf("contributor = %q, want %q", m.Contributor.Name, ContributorName)
	}
	if got := len(m.Intents); got != 17 {
		t.Errorf("intents = %d, want 17", got)
	}
}

// The React client refreshes through invalidates and nothing else, so each
// command's list is pinned exactly as the spec's intent table gives it.
func TestManifest_CommandInvalidates(t *testing.T) {
	want := map[string][]string{
		"collections.create":  {"collections.list", "system.overview"},
		"collections.update":  {"collections.list", "collections.get"},
		"collections.delete":  {"collections.list", "collections.get", "documents.list", "chunks.list", "system.overview"},
		"collections.reindex": {"collections.get", "system.overview"},
		"documents.ingest":    {"documents.list", "chunks.list", "collections.list", "collections.get", "system.overview"},
		"documents.delete":    {"documents.list", "documents.get", "chunks.list", "collections.list", "collections.get", "system.overview"},
		"retrieval.run":       nil,
		"retrieval.assemble":  nil,
	}
	seen := 0
	for _, in := range loadManifest(t).Intents {
		if in.Kind != dashcontract.IntentKindCommand {
			if len(in.Invalidates) != 0 {
				t.Errorf("query %s declares invalidates %v", in.Name, in.Invalidates)
			}
			continue
		}
		w, ok := want[in.Name]
		if !ok {
			t.Errorf("unexpected command %s", in.Name)
			continue
		}
		seen++
		if len(w) == 0 && len(in.Invalidates) == 0 {
			continue
		}
		if !reflect.DeepEqual(in.Invalidates, w) {
			t.Errorf("%s invalidates = %v, want %v", in.Name, in.Invalidates, w)
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of %d commands", seen, len(want))
	}
}
