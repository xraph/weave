package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/weave/internal/pgtest"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/postgres"
	"github.com/xraph/weave/store/storetest"
)

// TestConformance skips only when WEAVE_TEST_POSTGRES_DSN is unset. Once it
// is set, every failure is fatal: a skip and a failure look the same without
// -v, and a broken backend must not pass as merely untested.
func TestConformance(t *testing.T) {
	dsn := os.Getenv("WEAVE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_POSTGRES_DSN not set, skipping postgres")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		scoped := pgtest.Schema(t, dsn)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		drv := pgdriver.New()
		if err := drv.Open(ctx, scoped); err != nil {
			t.Fatalf("open postgres: %v", err)
		}
		db, err := grove.Open(drv)
		if err != nil {
			t.Fatalf("grove.Open postgres: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		s := postgres.New(db)
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("migrate postgres: %v", err)
		}
		return s
	})
}
