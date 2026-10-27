package mongo_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"

	"github.com/xraph/weave/document"
	"github.com/xraph/weave/internal/mongotest"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/mongo"
	"github.com/xraph/weave/store/storetest"
)

// TestConformance skips only when WEAVE_TEST_MONGO_DSN is unset; once set,
// every failure is fatal.
func TestConformance(t *testing.T) {
	dsn := os.Getenv("WEAVE_TEST_MONGO_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_MONGO_DSN not set, skipping mongo")
	}
	storetest.Run(t, opener(dsn))
}

// TestConformanceLegacy runs the cases that need rows a v1.8 install wrote.
func TestConformanceLegacy(t *testing.T) {
	dsn := os.Getenv("WEAVE_TEST_MONGO_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_MONGO_DSN not set, skipping mongo")
	}
	storetest.RunLegacy(t, opener(dsn), func(t *testing.T, s store.Store, doc *document.Document) {
		t.Helper()
		if err := mongo.PlantDocument(context.Background(), s.(*mongo.Store), doc); err != nil {
			t.Fatalf("plant document: %v", err)
		}
	})
}

// opener opens a fresh, migrated store for each case.
func opener(dsn string) storetest.Opener {
	return func(t *testing.T) store.Store {
		scoped := mongotest.Database(t, dsn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		drv := mongodriver.New()
		if err := drv.Open(ctx, scoped); err != nil {
			t.Fatalf("open mongo: %v", err)
		}
		db, err := grove.Open(drv)
		if err != nil {
			t.Fatalf("grove.Open mongo: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		s := mongo.New(db)
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("migrate mongo: %v", err)
		}
		return s
	}
}
