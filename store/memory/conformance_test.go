package memory_test

import (
	"context"
	"testing"

	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return memory.New() })
}

func TestConformanceLegacy(t *testing.T) {
	storetest.RunLegacy(t, func(*testing.T) store.Store { return memory.New() }, plant)
}

func plant(t *testing.T, s store.Store, doc *document.Document) {
	t.Helper()
	if err := memory.PlantDocument(context.Background(), s.(*memory.Store), doc); err != nil {
		t.Fatalf("plant document: %v", err)
	}
}
