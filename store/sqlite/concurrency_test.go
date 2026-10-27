package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/store/sqlite"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

// TestConcurrentKeyedIngest runs eight keyed ingests of one source at once,
// first with the same content and then with eight new contents, next to
// eight ingests of sources of their own and eight unkeyed ones. Every writer
// must wait its turn instead of failing with SQLITE_BUSY, and the shared
// source must end with one ready document.
func TestConcurrentKeyedIngest(t *testing.T) {
	const n = 8
	ctx := weave.WithTenant(context.Background(), "t1")
	s := opener()(t)
	vectors := vsmemory.New()
	e, err := engine.New(
		engine.WithStore(s),
		engine.WithVectorStore(vectors),
		engine.WithEmbedder(embedtest.New(64)),
		engine.WithChunker(chunker.NewRecursiveChunker()),
	)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	col := &collection.Collection{Name: "docs", ChunkSize: 512}
	if err = e.CreateCollection(ctx, col); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	round := func(name string, shared func(i int) string) {
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				in := &engine.IngestInput{CollectionID: col.ID, SourceKey: "shared", Content: shared(i)}
				if _, ingestErr := e.Ingest(ctx, in); ingestErr != nil {
					t.Errorf("%s: shared ingest %d: %v", name, i, ingestErr)
				}
			})
			wg.Go(func() {
				in := &engine.IngestInput{
					CollectionID: col.ID,
					SourceKey:    fmt.Sprintf("%s-own-%d", name, i),
					Content:      fmt.Sprintf("a page of its own, %s number %d", name, i),
				}
				if _, ingestErr := e.Ingest(ctx, in); ingestErr != nil {
					t.Errorf("%s: own ingest %d: %v", name, i, ingestErr)
				}
			})
			wg.Go(func() {
				in := &engine.IngestInput{CollectionID: col.ID, Content: fmt.Sprintf("an unkeyed page, %s number %d", name, i)}
				if _, ingestErr := e.Ingest(ctx, in); ingestErr != nil {
					t.Errorf("%s: unkeyed ingest %d: %v", name, i, ingestErr)
				}
			})
		}
		wg.Wait()

		docs, listErr := e.ListDocuments(ctx, &document.ListFilter{CollectionID: col.ID, SourceKey: "shared"})
		if listErr != nil {
			t.Fatalf("%s: list shared documents: %v", name, listErr)
		}
		ready := 0
		for _, d := range docs {
			if d.State == document.StateReady {
				ready++
			}
		}
		if ready != 1 || len(docs) != 1 {
			t.Errorf("%s: shared source holds %d documents, %d ready; want 1 ready", name, len(docs), ready)
		}
	}

	round("same", func(int) string { return "refunds are issued within thirty days" })
	round("new", func(i int) string { return fmt.Sprintf("refunds are issued within thirty days, edition %d", i) })

	total, err := e.CountDocuments(ctx, &document.CountFilter{CollectionID: col.ID})
	if err != nil {
		t.Fatalf("count documents: %v", err)
	}
	if want := int64(1 + 4*n); total != want {
		t.Errorf("documents: %d, want %d", total, want)
	}
}

// TestWriteTransactionHoldsTheLockFromTheStart opens a store write
// transaction and checks that another connection can't begin one of its
// own until it ends, as with BEGIN IMMEDIATE.
func TestWriteTransactionHoldsTheLockFromTheStart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "weave.db")
	drv := sqlitedriver.New()
	if err := drv.Open(ctx, path); err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := sqlite.New(db)
	if err = s.Migrate(ctx); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open second connection: %v", err)
	}
	t.Cleanup(func() { _ = other.Close() })
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	end, err := sqlite.BeginWrite(ctx, s)
	if err != nil {
		t.Fatalf("begin write: %v", err)
	}
	_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
	end()
	if err == nil {
		t.Fatal("BEGIN IMMEDIATE while the store writes: succeeded, want SQLITE_BUSY")
	}
	if !strings.Contains(err.Error(), "SQLITE_BUSY") {
		t.Fatalf("BEGIN IMMEDIATE while the store writes: %v, want SQLITE_BUSY", err)
	}
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("BEGIN IMMEDIATE after the store's transaction ended: %v", err)
	}
	if _, err = conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}
