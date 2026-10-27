package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
	_ "github.com/xraph/grove/drivers/sqlitedriver/sqlitemigrate" // registers the sqlite migrate executor

	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/sqlite"
	"github.com/xraph/weave/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, opener())
}

// TestConformanceLegacy runs the cases that need rows a v1.8 install wrote.
func TestConformanceLegacy(t *testing.T) {
	storetest.RunLegacy(t, opener(), func(t *testing.T, s store.Store, doc *document.Document) {
		t.Helper()
		if err := sqlite.PlantDocument(context.Background(), s.(*sqlite.Store), doc); err != nil {
			t.Fatalf("plant document: %v", err)
		}
	})
}

// opener opens a fresh, migrated store for each case.
func opener() storetest.Opener {
	return func(t *testing.T) store.Store {
		ctx := context.Background()
		drv := sqlitedriver.New()
		if err := drv.Open(ctx, filepath.Join(t.TempDir(), "weave.db")); err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		db, err := grove.Open(drv)
		if err != nil {
			t.Fatalf("grove.Open sqlite: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		s := sqlite.New(db)
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("migrate sqlite: %v", err)
		}
		return s
	}
}
