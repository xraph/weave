package mongo_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"
	"github.com/xraph/grove/drivers/mongodriver/mongomigrate"

	"github.com/xraph/weave"
	"github.com/xraph/weave/internal/mongotest"
	"github.com/xraph/weave/store/mongo"
)

// v18Versions are the migrations every install from before tracking ran.
var v18Versions = []string{"20240101000000", "20240101000001", "20240101000002"}

// TestMigrateRecordsEachMigrationOnce runs Migrate twice and expects one
// grove_migrations document per version in the group, so the second run
// applied nothing.
func TestMigrateRecordsEachMigrationOnce(t *testing.T) {
	ctx, db := openMigrateDB(t)
	s := mongo.New(db)

	for i := range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatalf("migrate %d: %v", i+1, err)
		}
	}

	assertAppliedOnce(ctx, t, mongodriver.Unwrap(db))
}

// TestMigrateAdoptsAnUntrackedInstall sets up the collections the way weave
// v1.8 did, by calling each Up with nothing recorded, then expects Migrate to
// record every version without failing and without touching existing
// documents.
func TestMigrateAdoptsAnUntrackedInstall(t *testing.T) {
	ctx, db := openMigrateDB(t)
	mdb := mongodriver.Unwrap(db)

	exec := mongomigrate.New(mdb)
	for _, m := range mongo.Migrations.Migrations() {
		if err := m.Up(ctx, exec); err != nil {
			t.Fatalf("untracked up %s: %v", m.Name, err)
		}
	}
	now := time.Now().UTC()
	if _, err := mdb.Database().Collection("weave_collections").InsertOne(ctx, bson.M{
		"_id":             "col_01h455vb4pex5vsknk084sn02q",
		"name":            "kept",
		"description":     "",
		"tenant_id":       "tenant-a",
		"app_id":          "app-a",
		"embedding_model": "text-embedding-3-small",
		"embedding_dims":  1536,
		"chunk_strategy":  "recursive",
		"chunk_size":      512,
		"chunk_overlap":   50,
		"metadata":        map[string]string{},
		"document_count":  int64(0),
		"chunk_count":     int64(0),
		"created_at":      now,
		"updated_at":      now,
	}); err != nil {
		t.Fatalf("insert untracked document: %v", err)
	}

	if err := mongo.New(db).Migrate(ctx); err != nil {
		t.Fatalf("migrate over an untracked install: %v", err)
	}

	assertAppliedOnce(ctx, t, mdb)

	count, err := mdb.Database().Collection("weave_collections").CountDocuments(ctx, bson.M{})
	if err != nil {
		t.Fatalf("count collections: %v", err)
	}
	if count != 1 {
		t.Fatalf("weave_collections holds %d documents after adoption, want 1", count)
	}
}

// TestMigrateErrorHasOnePrefix closes the database, then expects Migrate's
// error to match weave.ErrMigrationFailed and to start with a single
// "weave: " prefix.
func TestMigrateErrorHasOnePrefix(t *testing.T) {
	ctx, db := openMigrateDB(t)
	_ = db.Close()

	err := mongo.New(db).Migrate(ctx)
	if !errors.Is(err, weave.ErrMigrationFailed) {
		t.Fatalf("migrate on a closed database: %v, want weave.ErrMigrationFailed", err)
	}
	if !strings.HasPrefix(err.Error(), "weave: migration failed: ") {
		t.Fatalf("migrate error %q, want it to start with %q", err, "weave: migration failed: ")
	}
}

func openMigrateDB(t *testing.T) (context.Context, *grove.DB) {
	t.Helper()
	dsn := os.Getenv("WEAVE_TEST_MONGO_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_MONGO_DSN not set, skipping mongo")
	}
	scoped := mongotest.Database(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	drv := mongodriver.New()
	if err := drv.Open(ctx, scoped); err != nil {
		t.Fatalf("open mongo: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open mongo: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return ctx, db
}

// assertAppliedOnce checks that grove_migrations holds exactly one weave
// document for each version in mongo.Migrations, the v1.8 versions among
// them.
func assertAppliedOnce(ctx context.Context, t *testing.T, mdb *mongodriver.MongoDB) {
	t.Helper()
	applied, err := mongomigrate.New(mdb).ListApplied(ctx)
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

	migrations := mongo.Migrations.Migrations()
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
