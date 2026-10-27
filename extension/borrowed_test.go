package extension

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xraph/forge"
	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
	_ "github.com/xraph/grove/drivers/sqlitedriver/sqlitemigrate" // registers the sqlite migrate executor
	"github.com/xraph/vessel"

	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
)

func openGrove(t *testing.T) *grove.DB {
	t.Helper()
	drv := sqlitedriver.New()
	if err := drv.Open(context.Background(), filepath.Join(t.TempDir(), "weave.db")); err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestStopDoesNotCloseABorrowedDatabase(t *testing.T) {
	ctx := context.Background()
	db := openGrove(t)
	app := forge.New(forge.WithAppName("t"))
	if err := vessel.Provide(app.Container(), func() (*grove.DB, error) { return db, nil }); err != nil {
		t.Fatalf("provide db: %v", err)
	}

	ext := New(WithDisableRoutes())
	if err := ext.Register(app); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := ext.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := ext.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("Ping after Stop: %v, want the borrowed database still open", err)
	}
}

// closeCounter counts Close calls on the memory store it embeds.
type closeCounter struct {
	store.Store
	closed int
}

func (c *closeCounter) Close() error {
	c.closed++
	return c.Store.Close()
}

func TestStopClosesAStoreTheCallerPassed(t *testing.T) {
	ctx := context.Background()
	cs := &closeCounter{Store: memory.New()}
	ext := New(WithStore(cs), WithDisableRoutes())
	if err := ext.Register(forge.New(forge.WithAppName("t"))); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := ext.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := ext.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if cs.closed != 1 {
		t.Fatalf("Close called %d times, want 1", cs.closed)
	}
}

// The wrapper must expose every store.Store method, so a method added to
// the interface later cannot be hidden from the engine.
func TestBorrowedStoreExposesEveryStoreMethod(t *testing.T) {
	iface := reflect.TypeFor[store.Store]()
	got := reflect.TypeFor[borrowedStore]()
	for i := range iface.NumMethod() {
		name := iface.Method(i).Name
		if _, ok := got.MethodByName(name); !ok {
			t.Errorf("borrowedStore has no method %s", name)
		}
	}
	if !reflect.TypeFor[borrowedStore]().Implements(iface) {
		t.Fatal("borrowedStore does not implement store.Store")
	}
}
