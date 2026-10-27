package engine_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/internal/pgtest"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/store/postgres"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

// metadataStores opens a fresh metadata store per backend the retry tests
// run on: memory always, Postgres when WEAVE_TEST_POSTGRES_DSN is set.
func metadataStores(t *testing.T) map[string]func(t *testing.T) store.Store {
	t.Helper()
	backends := map[string]func(t *testing.T) store.Store{
		"memory": func(*testing.T) store.Store { return memory.New() },
	}
	dsn := os.Getenv("WEAVE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Log("WEAVE_TEST_POSTGRES_DSN not set, running on memory only")
		return backends
	}
	backends["postgres"] = func(t *testing.T) store.Store {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		drv := pgdriver.New()
		if err := drv.Open(ctx, pgtest.Schema(t, dsn)); err != nil {
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
	}
	return backends
}

// retryRig is an engine on meta and a memory vector store. stalledAfter is
// recovery's cutoff; a nanosecond makes every quiet document stalled.
type retryRig struct {
	Engine  *engine.Engine
	Meta    store.Store
	Vectors *vsmemory.Store
	Hooks   *failureHooks
}

func newRetryRig(t *testing.T, meta store.Store, stalledAfter time.Duration, wrap func(r *retryRig, s store.Store) store.Store) *retryRig {
	t.Helper()
	r := &retryRig{Meta: meta, Vectors: vsmemory.New(), Hooks: &failureHooks{}}
	s := meta
	if wrap != nil {
		s = wrap(r, meta)
	}
	e, err := engine.New(
		engine.WithStore(s),
		engine.WithVectorStore(r.Vectors),
		engine.WithEmbedder(embedtest.New(256)),
		engine.WithChunker(chunker.NewRecursiveChunker()),
		engine.WithExtension(r.Hooks),
		engine.WithRecovery(engine.RecoveryConfig{StalledAfter: stalledAfter}),
	)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	r.Engine = e
	return r
}

func (r *retryRig) ingest(colID id.CollectionID) (*engine.IngestResult, error) {
	return r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: colID, Title: "refunds", Content: sharedText})
}

// assertSearchable checks that res is a ready document whose row, chunks
// and vectors are in place and that Retrieve finds it.
func (r *retryRig) assertSearchable(t *testing.T, colID id.CollectionID, res *engine.IngestResult) {
	t.Helper()
	if res == nil || res.State != document.StateReady {
		t.Fatalf("retried ingest: %+v, want ready", res)
	}
	doc, err := r.Engine.GetDocument(tenantCtx("t1"), res.DocumentID)
	if err != nil || doc.State != document.StateReady {
		t.Fatalf("retried document: %+v, %v; want ready", doc, err)
	}
	hits, err := r.Engine.Retrieve(tenantCtx("t1"), sharedText, engine.WithCollection(colID), engine.WithTopK(10))
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("retrieve after the retry: no hits, want the retried document")
	}
	for _, h := range hits {
		if h.Chunk.DocumentID.String() != res.DocumentID.String() || !h.Hydrated {
			t.Errorf("hit from %s (hydrated %v), want only the retried document %s", h.Chunk.DocumentID, h.Hydrated, res.DocumentID)
		}
	}
}

// assertGone checks that docID has no row, no chunk rows and no vectors.
func (r *retryRig) assertGone(t *testing.T, docID id.DocumentID) {
	t.Helper()
	if _, err := r.Meta.GetDocument(tenantCtx("t1"), docID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("dead copy %s: get %v, want ErrDocumentNotFound", docID, err)
	}
	if n, err := r.Meta.CountChunks(tenantCtx("t1"), &chunk.CountFilter{DocumentID: docID}); err != nil || n != 0 {
		t.Errorf("dead copy %s: %d chunk rows (%v), want 0", docID, n, err)
	}
	if v := vectorsOf(t, r.Vectors, map[string]string{"document_id": docID.String()}); len(v) != 0 {
		t.Errorf("dead copy %s: %d vectors, want 0", docID, len(v))
	}
}

// The re-review's probe: an unkeyed ingest that recovery interrupts leaves
// a failed row holding its content hash. Retrying the same content used to
// be ErrDuplicateDocument forever, with nothing searchable. Now the failed
// row goes and the retry ends ready.
func TestUnkeyedRetryAfterRecoveryEndsReady(t *testing.T) {
	for name, open := range metadataStores(t) {
		t.Run(name, func(t *testing.T) {
			var first atomic.Bool
			first.Store(true)
			r := newRetryRig(t, open(t), time.Nanosecond, func(r *retryRig, s store.Store) store.Store {
				return readyStore{Store: s, before: func(context.Context, id.DocumentID) error {
					if !first.CompareAndSwap(true, false) {
						return nil
					}
					settle()
					if res, err := r.Engine.RecoverStalled(context.Background(), 0); err != nil || res.Failed != 1 {
						t.Errorf("RecoverStalled before the ready write: %+v, %v; want one failed", res, err)
					}
					return nil
				}}
			})
			col := mustCollectionAs(t, tenantCtx("t1"), r.Engine, "docs")

			failed, err := r.ingest(col.ID)
			if !errors.Is(err, weave.ErrIngestInterrupted) {
				t.Fatalf("first ingest: %v, want ErrIngestInterrupted", err)
			}
			retried, err := r.ingest(col.ID)
			if err != nil {
				t.Fatalf("retry the same content: %v", err)
			}
			r.assertSearchable(t, col.ID, retried)
			r.assertGone(t, failed.DocumentID)
		})
	}
}

// An ingest whose ready write fails with a store error ends failed with its
// chunks and vectors gone, so nothing of it is searchable, and the retry
// of the same content goes through.
func TestUnkeyedRetryAfterAFailedReadyWriteEndsReady(t *testing.T) {
	errReset := errors.New("connection reset by peer")
	for name, open := range metadataStores(t) {
		t.Run(name, func(t *testing.T) {
			var first atomic.Bool
			first.Store(true)
			r := newRetryRig(t, open(t), time.Nanosecond, func(_ *retryRig, s store.Store) store.Store {
				return readyStore{Store: s, before: func(context.Context, id.DocumentID) error {
					if first.CompareAndSwap(true, false) {
						return errReset
					}
					return nil
				}}
			})
			col := mustCollectionAs(t, tenantCtx("t1"), r.Engine, "docs")

			failed, err := r.ingest(col.ID)
			if !errors.Is(err, errReset) {
				t.Fatalf("first ingest: %v, want the store's error", err)
			}
			doc, getErr := r.Meta.GetDocument(tenantCtx("t1"), failed.DocumentID)
			if getErr != nil || doc.State != document.StateFailed {
				t.Fatalf("first document: %+v, %v; want failed", doc, getErr)
			}
			if n, countErr := r.Meta.CountChunks(tenantCtx("t1"), &chunk.CountFilter{DocumentID: failed.DocumentID}); countErr != nil || n != 0 {
				t.Errorf("failed document: %d chunk rows (%v), want 0", n, countErr)
			}
			if v := vectorsOf(t, r.Vectors, map[string]string{"document_id": failed.DocumentID.String()}); len(v) != 0 {
				t.Errorf("failed document: %d vectors, want 0", len(v))
			}
			hits, retrieveErr := r.Engine.Retrieve(tenantCtx("t1"), sharedText, engine.WithCollection(col.ID))
			if retrieveErr != nil || len(hits) != 0 {
				t.Errorf("retrieve after the failed ingest: %d hits (%v), want none", len(hits), retrieveErr)
			}

			retried, err := r.ingest(col.ID)
			if err != nil {
				t.Fatalf("retry the same content: %v", err)
			}
			r.assertSearchable(t, col.ID, retried)
			r.assertGone(t, failed.DocumentID)
		})
	}
}

// A stalled pending document of the same content (its process died) is
// failed with recovery's guarded write, IngestFailed fires once for it,
// and the new ingest takes the content over.
func TestUnkeyedIngestReplacesAStalledCopy(t *testing.T) {
	for name, open := range metadataStores(t) {
		t.Run(name, func(t *testing.T) {
			r := newRetryRig(t, open(t), time.Nanosecond, nil)
			col := mustCollectionAs(t, tenantCtx("t1"), r.Engine, "docs")
			stalled := &document.Document{
				Entity: weave.NewEntity(), ID: id.NewDocumentID(), CollectionID: col.ID, TenantID: "t1",
				ContentHash: hashOf(sharedText), ContentLength: len(sharedText), State: document.StatePending,
			}
			if err := r.Meta.CreateDocument(tenantCtx("t1"), stalled); err != nil {
				t.Fatalf("plant the stalled copy: %v", err)
			}
			settle()

			res, err := r.ingest(col.ID)
			if err != nil {
				t.Fatalf("ingest over a stalled copy: %v", err)
			}
			r.assertSearchable(t, col.ID, res)
			r.assertGone(t, stalled.ID)
			if got := r.Hooks.got(); len(got) != 1 || !errors.Is(got[0], weave.ErrIngestInterrupted) {
				t.Errorf("IngestFailed hooks: %v, want one ErrIngestInterrupted for the stalled copy", got)
			}
		})
	}
}

// Content a live document holds is still a duplicate: a ready one, or one
// another ingest is working on (a heartbeat inside StalledAfter).
func TestUnkeyedIngestOfLiveContentIsADuplicate(t *testing.T) {
	for name, open := range metadataStores(t) {
		t.Run(name, func(t *testing.T) {
			r := newRetryRig(t, open(t), time.Nanosecond, nil)
			col := mustCollectionAs(t, tenantCtx("t1"), r.Engine, "docs")
			ready, err := r.ingest(col.ID)
			if err != nil {
				t.Fatalf("first ingest: %v", err)
			}
			if _, err = r.ingest(col.ID); !errors.Is(err, weave.ErrDuplicateDocument) {
				t.Errorf("ingest content a ready document holds: %v, want ErrDuplicateDocument", err)
			}
			if doc, getErr := r.Meta.GetDocument(tenantCtx("t1"), ready.DocumentID); getErr != nil || doc.State != document.StateReady {
				t.Errorf("ready document after the duplicate: %+v, %v; want it untouched", doc, getErr)
			}

			// A fresh in-flight copy under the default cutoff.
			live := newRetryRig(t, open(t), engine.DefaultStalledAfter, nil)
			liveCol := mustCollectionAs(t, tenantCtx("t1"), live.Engine, "docs")
			inFlight := &document.Document{
				Entity: weave.NewEntity(), ID: id.NewDocumentID(), CollectionID: liveCol.ID, TenantID: "t1",
				ContentHash: hashOf(sharedText), ContentLength: len(sharedText), State: document.StateProcessing,
			}
			if err = live.Meta.CreateDocument(tenantCtx("t1"), inFlight); err != nil {
				t.Fatalf("plant the in-flight copy: %v", err)
			}
			if _, err = live.ingest(liveCol.ID); !errors.Is(err, weave.ErrDuplicateDocument) {
				t.Errorf("ingest content an in-flight document holds: %v, want ErrDuplicateDocument", err)
			}
			if doc, getErr := live.Meta.GetDocument(tenantCtx("t1"), inFlight.ID); getErr != nil || doc.State != document.StateProcessing {
				t.Errorf("in-flight document after the duplicate: %+v, %v; want it untouched", doc, getErr)
			}
			if got := live.Hooks.got(); len(got) != 0 {
				t.Errorf("IngestFailed hooks: %v, want none", got)
			}
		})
	}
}

// hashOf is the content hash ingest gives content.
func hashOf(content string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(content))) }
