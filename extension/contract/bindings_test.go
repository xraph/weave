package contract

import (
	"sort"
	"testing"
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
