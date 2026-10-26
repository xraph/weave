package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"

	"github.com/xraph/weave"
	"github.com/xraph/weave/store/sqlite"
)

// TestMigrateErrorHasOnePrefix closes the database, then expects Migrate's
// error to match weave.ErrMigrationFailed and to start with a single
// "weave: " prefix.
func TestMigrateErrorHasOnePrefix(t *testing.T) {
	ctx := context.Background()
	drv := sqlitedriver.New()
	if err := drv.Open(ctx, filepath.Join(t.TempDir(), "weave.db")); err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open sqlite: %v", err)
	}
	_ = db.Close()

	err = sqlite.New(db).Migrate(ctx)
	if !errors.Is(err, weave.ErrMigrationFailed) {
		t.Fatalf("migrate on a closed database: %v, want weave.ErrMigrationFailed", err)
	}
	if !strings.HasPrefix(err.Error(), "weave: migration failed: ") {
		t.Fatalf("migrate error %q, want it to start with %q", err, "weave: migration failed: ")
	}
}
