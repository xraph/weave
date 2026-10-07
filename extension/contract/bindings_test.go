package contract

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
)

// Register refuses a handler the manifest does not declare; this closes the
// other direction, so a declared intent never answers "no handler".
func TestEveryDeclaredIntentIsBound(t *testing.T) {
	bound := map[string]bool{}
	for _, b := range bindings(newDeps(t, openMemory(t))) {
		if bound[b.intent] {
			t.Errorf("%s is bound twice", b.intent)
		}
		bound[b.intent] = true
	}
	var missing []string
	for _, in := range loadManifest(t).Intents {
		if !bound[in.Name] {
			missing = append(missing, in.Name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("declared but not bound: %v", missing)
	}
}

// A binding the manifest does not declare is refused before the manifest
// is registered, so the registry never holds a contributor half wired.
func TestRegister_UndeclaredBindingRegistersNothing(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	raw := bytes.Replace(manifestYAML, []byte("  - { name: chunks.get,"), []byte("  - { name: chunks.got,"), 1)
	if bytes.Equal(raw, manifestYAML) {
		t.Fatal("the manifest edit did not apply; the test would prove nothing")
	}
	reg := dashcontract.NewRegistry()
	err := register(dispatcher.New(nil), reg, dashcontract.NewWardenRegistry(), raw, bindings(deps))
	if err == nil || !strings.Contains(err.Error(), "chunks.get is bound but the manifest does not declare it") {
		t.Fatalf("register: %v, want the undeclared binding refused", err)
	}
	if _, ok := reg.Contributor(ContributorName); ok {
		t.Error("the contributor was registered before the binding check failed")
	}
}
