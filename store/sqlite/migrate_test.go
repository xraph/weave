package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
	_ "github.com/xraph/grove/drivers/sqlitedriver/sqlitemigrate" // registers the sqlite migrate executor
	"github.com/xraph/grove/migrate"

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

// TestMigrateRerunsOverAnUntrackedInstall runs every Up twice with nothing
// recorded, the way a v1.8 start did, then expects Migrate to succeed. SQLite
// has no ADD COLUMN IF NOT EXISTS, so the source key migration looks the
// column up before adding it.
func TestMigrateRerunsOverAnUntrackedInstall(t *testing.T) {
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

	exec, err := migrate.NewExecutorFor(drv)
	if err != nil {
		t.Fatalf("migration executor: %v", err)
	}
	for run := range 2 {
		for _, m := range sqlite.Migrations.Migrations() {
			if err := m.Up(ctx, exec); err != nil {
				t.Fatalf("untracked up %s, run %d: %v", m.Name, run+1, err)
			}
		}
	}
	if err := sqlite.New(db).Migrate(ctx); err != nil {
		t.Fatalf("migrate over an untracked install: %v", err)
	}
}
