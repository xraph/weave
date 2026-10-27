package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store/memory"
)

// readyStore is a memory store that runs before ahead of the move to ready
// that ends an unkeyed ingest, and fails that move with before's error.
type readyStore struct {
	*memory.Store
	before func(ctx context.Context, docID id.DocumentID) error
}

func (s readyStore) CompleteDocument(ctx context.Context, docID id.DocumentID, chunkCount int) error {
	if err := s.before(ctx, docID); err != nil {
		return err
	}
	return s.Store.CompleteDocument(ctx, docID, chunkCount)
}

// The final ready write can fail on its own: a transient store error on
// that one write. Ingest used to swallow it and report ready while the row
// stayed processing, and recovery later failed it and deleted its data.
// Now the caller gets the error, the document is failed, and recovery has
// nothing left to do.
func TestIngestReportsAFailedReadyWrite(t *testing.T) {
	hooks := &failureHooks{}
	errReset := errors.New("connection reset by peer")
	r := newRig(t, withOpt(engine.WithExtension(hooks)), func(r *testRig) engine.Option {
		return engine.WithStore(readyStore{Store: r.Store, before: func(context.Context, id.DocumentID) error { return errReset }})
	})
	col := mustTestCollection(t, r.Engine, "docs")

	res, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, Title: "refunds", Content: sharedText})
	if !errors.Is(err, errReset) {
		t.Fatalf("ingest whose ready write fails: got %v, want the store's error", err)
	}
	if res == nil || res.State != document.StateFailed {
		t.Fatalf("ingest whose ready write fails: result %+v, want failed", res)
	}
	doc, getErr := r.Engine.GetDocument(tenantCtx("t1"), res.DocumentID)
	if getErr != nil {
		t.Fatalf("get the document: %v", getErr)
	}
	if doc.State != document.StateFailed || doc.Error == "" {
		t.Errorf("document: state %q error %q, want failed with the error", doc.State, doc.Error)
	}
	if got := hooks.got(); len(got) != 1 || !errors.Is(got[0], errReset) {
		t.Errorf("IngestFailed hooks: %v, want one with the store's error", got)
	}

	settle()
	rec, recErr := r.Engine.RecoverStalled(context.Background(), 0)
	if recErr != nil || rec != (engine.RecoveryResult{}) {
		t.Errorf("RecoverStalled after the failed ingest: %+v, %v; want nothing to do", rec, recErr)
	}
}

// Recovery can fail the document after the last heartbeat and before the
// ready write. The write is guarded, so the document stays failed, and the
// ingest takes back what it wrote and answers ErrIngestInterrupted.
func TestIngestDoesNotTurnARecoveredDocumentReady(t *testing.T) {
	hooks := &failureHooks{}
	var recovered id.DocumentID
	r := newRig(t, withOpt(engine.WithExtension(hooks)), func(r *testRig) engine.Option {
		return engine.WithStore(readyStore{Store: r.Store, before: func(_ context.Context, docID id.DocumentID) error {
			recovered = docID
			settle()
			res, err := r.Engine.RecoverStalled(context.Background(), 0)
			if err != nil || res.Failed != 1 {
				t.Errorf("RecoverStalled before the ready write: %+v, %v; want one failed", res, err)
			}
			return nil
		}})
	})
	col := mustTestCollection(t, r.Engine, "docs")

	_, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, Title: "refunds", Content: sharedText})
	checkInterrupted(t, r, recovered, err)
	if got := hooks.got(); len(got) != 1 || !errors.Is(got[0], weave.ErrIngestInterrupted) {
		t.Errorf("IngestFailed hooks: %v, want recovery's one", got)
	}
}

// A request cancelled right at the end still gets its document ready: the
// write runs on a context the caller's cancellation doesn't reach.
func TestIngestCancelledAtTheEndTurnsReady(t *testing.T) {
	ctx, cancel := context.WithCancel(tenantCtx("t1"))
	defer cancel()
	r := newRig(t, func(r *testRig) engine.Option {
		return engine.WithStore(readyStore{Store: r.Store, before: func(writeCtx context.Context, _ id.DocumentID) error {
			cancel()
			return writeCtx.Err()
		}})
	})
	col := mustTestCollection(t, r.Engine, "docs")

	res, err := r.Engine.Ingest(ctx, &engine.IngestInput{CollectionID: col.ID, Title: "refunds", Content: sharedText})
	if err != nil || res.State != document.StateReady {
		t.Fatalf("ingest cancelled before the ready write: %+v, %v; want ready", res, err)
	}
	doc, getErr := r.Engine.GetDocument(tenantCtx("t1"), res.DocumentID)
	if getErr != nil || doc.State != document.StateReady || doc.ChunkCount != res.ChunkCount {
		t.Errorf("document: %+v, %v; want ready with %d chunks", doc, getErr, res.ChunkCount)
	}
}
