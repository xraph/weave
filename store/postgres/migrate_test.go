package postgres_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"

	"github.com/xraph/weave"
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

// TestMigrateConcurrentFirstBoot starts eight replicas on one empty schema at
// once, each with its own connection pool, and expects every Migrate to
// succeed with one row per version. Grove creates its tracking tables before
// it takes the migration lock, so the replicas race on that CREATE.
func TestMigrateConcurrentFirstBoot(t *testing.T) {
	ctx, scoped := migrateSchema(t)

	const replicas = 8
	dbs := make([]*grove.DB, replicas)
	for i := range dbs {
		dbs[i] = openDB(ctx, t, scoped)
	}

	errs := make([]error, replicas)
	var wg sync.WaitGroup
	for i, db := range dbs {
		wg.Go(func() { errs[i] = postgres.New(db).Migrate(ctx) })
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d migrate: %v", i, err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	assertAppliedOnce(ctx, t, pgdriver.Unwrap(dbs[0]))
}

// TestMigrateErrorHasOnePrefix closes the database, then expects Migrate's
// error to match weave.ErrMigrationFailed and to start with a single
// "weave: " prefix.
func TestMigrateErrorHasOnePrefix(t *testing.T) {
	ctx, db := openMigrateDB(t)
	_ = db.Close()

	err := postgres.New(db).Migrate(ctx)
	if !errors.Is(err, weave.ErrMigrationFailed) {
		t.Fatalf("migrate on a closed database: %v, want weave.ErrMigrationFailed", err)
	}
	if !strings.HasPrefix(err.Error(), "weave: migration failed: ") {
		t.Fatalf("migrate error %q, want it to start with %q", err, "weave: migration failed: ")
	}
}

func openMigrateDB(t *testing.T) (context.Context, *grove.DB) {
	t.Helper()
	ctx, scoped := migrateSchema(t)
	return ctx, openDB(ctx, t, scoped)
}

// migrateSchema returns a context bounded for one test and a DSN pinned to a
// fresh schema of its own.
func migrateSchema(t *testing.T) (context.Context, string) {
	t.Helper()
	dsn := os.Getenv("WEAVE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_POSTGRES_DSN not set, skipping postgres")
	}
	scoped := pgtest.Schema(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx, scoped
}

func openDB(ctx context.Context, t *testing.T, dsn string) *grove.DB {
	t.Helper()
	drv := pgdriver.New()
	if err := drv.Open(ctx, dsn); err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
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
