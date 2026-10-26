package postgres_test

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"

	"github.com/xraph/weave/internal/pgtest"
	"github.com/xraph/weave/store/postgres"
)

// v18Versions are the migrations every install from before tracking ran.
var v18Versions = []string{"20240101000000", "20240101000001", "20240101000002"}

// TestMigrateRecordsEachMigrationOnce runs Migrate twice and expects one
// grove_migrations row per version in the group, so the second run applied
// nothing.
func TestMigrateRecordsEachMigrationOnce(t *testing.T) {
	ctx, db := openMigrateDB(t)
	s := postgres.New(db)

	for i := range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("migrate %d: %v", i+1, err)
		}
	}

	assertAppliedOnce(ctx, t, pgdriver.Unwrap(db))
}

// TestMigrateAdoptsAnUntrackedInstall sets up the schema the way weave v1.8
// did, by calling each Up with nothing recorded, then expects Migrate to
// record every version without failing and without touching existing rows.
func TestMigrateAdoptsAnUntrackedInstall(t *testing.T) {
	ctx, db := openMigrateDB(t)
	pg := pgdriver.Unwrap(db)

	exec := pgmigrate.New(pg)
	for _, m := range postgres.Migrations.Migrations() {
		if err := m.Up(ctx, exec); err != nil {
			t.Fatalf("untracked up %s: %v", m.Name, err)
		}
	}
	if _, err := pg.Exec(ctx, `INSERT INTO weave_collections (id, name, tenant_id, app_id)
VALUES ('col_01h455vb4pex5vsknk084sn02q', 'kept', 'tenant-a', 'app-a')`); err != nil {
		t.Fatalf("insert untracked row: %v", err)
	}

	if err := postgres.New(db).Migrate(ctx); err != nil {
		t.Fatalf("migrate over an untracked install: %v", err)
	}

	assertAppliedOnce(ctx, t, pg)

	var count int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM weave_collections`).Scan(&count); err != nil {
		t.Fatalf("count collections: %v", err)
	}
	if count != 1 {
		t.Fatalf("weave_collections holds %d rows after adoption, want 1", count)
	}
}

func openMigrateDB(t *testing.T) (context.Context, *grove.DB) {
	t.Helper()
	dsn := os.Getenv("WEAVE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_POSTGRES_DSN not set, skipping postgres")
	}
	scoped := pgtest.Schema(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	drv := pgdriver.New()
	if err := drv.Open(ctx, scoped); err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db
}

// assertAppliedOnce checks that grove_migrations holds exactly one weave row
// for each version in postgres.Migrations, the v1.8 versions among them.
func assertAppliedOnce(ctx context.Context, t *testing.T, pg *pgdriver.PgDB) {
	t.Helper()
	applied, err := pgmigrate.New(pg).ListApplied(ctx)
	if err != nil {
		t.Fatalf("list applied: %v", err)
	}
	var got []string
	for _, a := range applied {
		if a.Group == "weave" {
			got = append(got, a.Version)
		}
	}
	slices.Sort(got)

	migrations := postgres.Migrations.Migrations()
	want := make([]string, 0, len(migrations))
	for _, m := range migrations {
		want = append(want, m.Version)
	}
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("applied weave versions = %v, want %v", got, want)
	}
	for _, v := range v18Versions {
		if !slices.Contains(got, v) {
			t.Fatalf("applied weave versions %v lack %s", got, v)
		}
	}
}
