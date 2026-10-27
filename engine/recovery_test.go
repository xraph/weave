package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/vectorstore"
)

// settle lets the clock move past the last write, so a cutoff of "now"
// (stalledAfter 0) is strictly after it.
func settle() { time.Sleep(2 * time.Millisecond) }

// failureHooks records the error of every IngestFailed hook.
type failureHooks struct {
	mu   sync.Mutex
	errs []error
}

func (h *failureHooks) Name() string { return "failure-hooks" }
func (h *failureHooks) OnIngestFailed(_ context.Context, _ id.CollectionID, err error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.errs = append(h.errs, err)
	return nil
}

func (h *failureHooks) got() []error {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]error(nil), h.errs...)
}

// plantCrashed writes, through the stores, what an ingest killed after its
// chunk and vector writes leaves: a document in state with n chunk rows and
// n vectors labelled with it.
func plantCrashed(t *testing.T, r *testRig, col *collection.Collection, state document.State, n int) *document.Document {
	t.Helper()
	ctx := tenantCtx(col.TenantID)
	doc := &document.Document{
		Entity: weave.NewEntity(), ID: id.NewDocumentID(), CollectionID: col.ID, TenantID: col.TenantID,
		ContentHash: "sha-" + id.NewDocumentID().String(), State: state,
	}
	if err := r.Store.CreateDocument(ctx, doc); err != nil {
		t.Fatalf("plant a %s document: %v", state, err)
	}
	if n == 0 {
		return doc
	}
	chunks := make([]*chunk.Chunk, n)
	entries := make([]vectorstore.Entry, n)
	for i := range chunks {
		chunks[i] = &chunk.Chunk{
			ID: id.NewChunkID(), DocumentID: doc.ID, CollectionID: col.ID, TenantID: col.TenantID,
			Content: "half a document", Index: i, Metadata: map[string]string{},
		}
		vec := make([]float32, 256)
		vec[i] = 1
		entries[i] = vectorstore.Entry{ID: chunks[i].ID.String(), Vector: vec, Content: "half a document", Metadata: map[string]string{
			"tenant_id": col.TenantID, "collection_id": col.ID.String(), "document_id": doc.ID.String(),
		}}
	}
	if state != document.StateSuperseded {
		if err := r.Store.CreateChunkBatch(ctx, chunks); err != nil {
			t.Fatalf("plant chunks: %v", err)
		}
	}
	if err := r.Vectors.Upsert(ctx, entries); err != nil {
		t.Fatalf("plant vectors: %v", err)
	}
	return doc
}

// assertNoData checks that docID has no chunk rows and no vectors left.
func assertNoData(t *testing.T, r *testRig, tenant string, docID id.DocumentID) {
	t.Helper()
	n, err := r.Store.CountChunks(tenantCtx(tenant), &chunk.CountFilter{DocumentID: docID})
	if err != nil {
		t.Fatalf("count chunks: %v", err)
	}
	if n != 0 {
		t.Errorf("document %s: %d chunk rows left, want 0", docID, n)
	}
	if v := vectorsOf(t, r.Vectors, map[string]string{"document_id": docID.String()}); len(v) != 0 {
		t.Errorf("document %s: %d vectors left, want 0", docID, len(v))
	}
}

func TestRecoverMarksStalledFailedAndPurgesPartialData(t *testing.T) {
	hooks := &failureHooks{}
	r := newRig(t, withOpt(engine.WithExtension(hooks)))
	col := mustTestCollection(t, r.Engine, "docs")
	crashed := plantCrashed(t, r, col, document.StateProcessing, 2)
	// Another tenant's finished document stays as it is.
	other := mustCollectionAs(t, tenantCtx("t2"), r.Engine, "docs")
	kept := plantCrashed(t, r, other, document.StateReady, 2)
	settle()

	// No tenant on the context: recovery is an operator's sweep.
	res, err := r.Engine.RecoverStalled(context.Background(), 0)
	if err != nil {
		t.Fatalf("RecoverStalled: %v", err)
	}
	if res != (engine.RecoveryResult{Failed: 1}) {
		t.Errorf("RecoverStalled: %+v, want {Failed:1}", res)
	}
	doc, err := r.Engine.GetDocument(tenantCtx("t1"), crashed.ID)
	if err != nil {
		t.Fatalf("get the recovered document: %v", err)
	}
	if doc.State != document.StateFailed || !strings.Contains(doc.Error, "interrupted") {
		t.Errorf("recovered document: state %q error %q, want failed and interrupted", doc.State, doc.Error)
	}
	assertNoData(t, r, "t1", crashed.ID)
	if got := hooks.got(); len(got) != 1 || !errors.Is(got[0], weave.ErrIngestInterrupted) {
		t.Errorf("IngestFailed hooks: %v, want one ErrIngestInterrupted", got)
	}

	if v := r.Vectors.Len(); v != 2 {
		t.Errorf("vectors left: %d, want t2's 2", v)
	}
	if n, err := r.Store.CountChunks(tenantCtx("t2"), &chunk.CountFilter{DocumentID: kept.ID}); err != nil || n != 2 {
		t.Errorf("t2's document: %d chunk rows (%v), want 2", n, err)
	}
}

func TestRecoverPurgesSupersededDocuments(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")
	old := plantCrashed(t, r, col, document.StateSuperseded, 2)
	settle()

	res, err := r.Engine.RecoverStalled(context.Background(), 0)
	if err != nil {
		t.Fatalf("RecoverStalled: %v", err)
	}
	if res != (engine.RecoveryResult{Purged: 1}) {
		t.Errorf("RecoverStalled: %+v, want {Purged:1}", res)
	}
	if _, err := r.Engine.GetDocument(tenantCtx("t1"), old.ID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("superseded document after recovery: %v, want ErrDocumentNotFound", err)
	}
	assertNoData(t, r, "t1", old.ID)
}

func TestRecoverLeavesFreshAndSettledDocumentsAlone(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")
	docs := []*document.Document{
		plantCrashed(t, r, col, document.StateProcessing, 1),
		plantCrashed(t, r, col, document.StateReady, 1),
		plantCrashed(t, r, col, document.StateFailed, 1),
	}
	vectorsBefore := r.Vectors.Len()

	res, err := r.Engine.RecoverStalled(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("RecoverStalled: %v", err)
	}
	if res != (engine.RecoveryResult{}) {
		t.Errorf("RecoverStalled: %+v, want nothing done", res)
	}
	for _, want := range docs {
		got, err := r.Engine.GetDocument(tenantCtx("t1"), want.ID)
		if err != nil {
			t.Fatalf("get %s document: %v", want.State, err)
		}
		if got.State != want.State || got.Error != want.Error {
			t.Errorf("%s document: state %q error %q after recovery", want.State, got.State, got.Error)
		}
		if n, err := r.Store.CountChunks(tenantCtx("t1"), &chunk.CountFilter{DocumentID: want.ID}); err != nil || n != 1 {
			t.Errorf("%s document: %d chunk rows (%v), want 1", want.State, n, err)
		}
	}
	if v := r.Vectors.Len(); v != vectorsBefore {
		t.Errorf("vectors: %d after recovery, want %d", v, vectorsBefore)
	}
}

// recordingStore is a memory store that counts the stalled listings and
// every write, and can add rows to the stalled listing.
type recordingStore struct {
	*memory.Store
	extra    []*document.Document
	listings atomic.Int64
	writes   atomic.Int64
}

func (s *recordingStore) ListStalledDocuments(ctx context.Context, before time.Time, limit int) ([]*document.Document, error) {
	s.listings.Add(1)
	docs, err := s.Store.ListStalledDocuments(ctx, before, limit)
	return append(docs, s.extra...), err
}

func (s *recordingStore) UpdateDocument(ctx context.Context, doc *document.Document) error {
	s.writes.Add(1)
	return s.Store.UpdateDocument(ctx, doc)
}

func (s *recordingStore) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	s.writes.Add(1)
	return s.Store.DeleteDocument(ctx, docID)
}

func (s *recordingStore) DeleteChunksByDocument(ctx context.Context, docID id.DocumentID) error {
	s.writes.Add(1)
	return s.Store.DeleteChunksByDocument(ctx, docID)
}

func (s *recordingStore) TouchDocument(ctx context.Context, docID id.DocumentID, staleBefore time.Time) error {
	s.writes.Add(1)
	return s.Store.TouchDocument(ctx, docID, staleBefore)
}

func (s *recordingStore) FailDocument(ctx context.Context, docID id.DocumentID, errText string, staleBefore time.Time) error {
	s.writes.Add(1)
	return s.Store.FailDocument(ctx, docID, errText, staleBefore)
}

func (s *recordingStore) calls() int64 { return s.listings.Load() + s.writes.Load() }

func TestRecoverSkipsRowsWithoutATenant(t *testing.T) {
	recording := &recordingStore{}
	r := newRig(t, func(r *testRig) engine.Option { recording.Store = r.Store; return engine.WithStore(recording) })
	// A row written before v1.9, when the tenant could be empty.
	recording.extra = []*document.Document{{
		Entity: weave.NewEntity(), ID: id.NewDocumentID(), CollectionID: id.NewCollectionID(),
		State: document.StateProcessing,
	}}

	res, err := r.Engine.RecoverStalled(context.Background(), 0)
	if err != nil {
		t.Fatalf("RecoverStalled: %v", err)
	}
	if res != (engine.RecoveryResult{SkippedNoTenant: 1}) {
		t.Errorf("RecoverStalled: %+v, want {SkippedNoTenant:1}", res)
	}
	if n := recording.writes.Load(); n != 0 {
		t.Errorf("store: %d writes for a row without a tenant, want 0", n)
	}
}

func TestReingestAfterRecoverySucceeds(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")
	stuck := plantProcessing(t, r, col.ID)
	settle()

	if res, err := r.Engine.RecoverStalled(context.Background(), 0); err != nil || res.Failed != 1 {
		t.Fatalf("RecoverStalled: %+v, %v; want one failed", res, err)
	}
	res, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionOne})
	if err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	if res.Unchanged || res.State != document.StateReady {
		t.Errorf("re-ingest: %+v, want a new ready document", res)
	}
	if _, err := r.Engine.GetDocument(tenantCtx("t1"), stuck.ID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("the failed copy after the re-ingest: %v, want ErrDocumentNotFound", err)
	}
	if docs := sourceDocs(t, r.Engine, col.ID, "a"); len(docs) != 1 || docs[0].ID.String() != res.DocumentID.String() {
		t.Errorf("source after the re-ingest: %+v, want only %s", docs, res.DocumentID)
	}
}

// processingDoc returns the one processing document in colID.
func processingDoc(t *testing.T, r *testRig, colID id.CollectionID) *document.Document {
	t.Helper()
	docs, err := r.Engine.ListDocuments(tenantCtx("t1"), &document.ListFilter{CollectionID: colID, State: document.StateProcessing})
	if err != nil || len(docs) != 1 {
		t.Fatalf("processing documents: %+v, %v; want one", docs, err)
	}
	return docs[0]
}

// checkInterrupted checks that an ingest recovery overtook ended with
// ErrIngestInterrupted and left its document failed with nothing of it in
// either store.
func checkInterrupted(t *testing.T, r *testRig, docID id.DocumentID, err error) {
	t.Helper()
	if !errors.Is(err, weave.ErrIngestInterrupted) {
		t.Errorf("ingest after recovery failed its document: got %v, want ErrIngestInterrupted", err)
	}
	doc, getErr := r.Engine.GetDocument(tenantCtx("t1"), docID)
	if getErr != nil {
		t.Fatalf("get the document: %v", getErr)
	}
	if doc.State != document.StateFailed {
		t.Errorf("document: state %q, want it to stay failed", doc.State)
	}
	assertNoData(t, r, "t1", docID)
}

func TestRecoveredIngestDoesNotTurnReady(t *testing.T) {
	for _, keyed := range []bool{false, true} {
		name := "unkeyed"
		if keyed {
			name = "keyed"
		}
		t.Run(name, func(t *testing.T) {
			gate := newGateEmbedder()
			hooks := &failureHooks{}
			r := newRig(t, withOpt(engine.WithEmbedder(gate)), withOpt(engine.WithExtension(hooks)))
			col := mustTestCollection(t, r.Engine, "docs")
			input := &engine.IngestInput{CollectionID: col.ID, Content: versionOne}
			if keyed {
				input.SourceKey = "a"
			}
			done := make(chan error, 1)
			go func() {
				_, err := r.Engine.Ingest(tenantCtx("t1"), input)
				done <- err
			}()
			<-gate.entered
			doc := processingDoc(t, r, col.ID)
			settle()

			if res, err := r.Engine.RecoverStalled(context.Background(), 0); err != nil || res.Failed != 1 {
				t.Fatalf("RecoverStalled during the embed: %+v, %v; want one failed", res, err)
			}
			close(gate.release)
			checkInterrupted(t, r, doc.ID, <-done)
			// Recovery fired it; the interrupted ingest adds none of its own.
			if got := hooks.got(); len(got) != 1 || !errors.Is(got[0], weave.ErrIngestInterrupted) {
				t.Errorf("IngestFailed hooks: %v, want recovery's one", got)
			}
		})
	}
}

// The last heartbeat comes after the unkeyed path has written its chunk rows
// and vectors, so a document recovery fails in that window never turns
// ready, and the ingest takes back what it wrote.
func TestRecoveryBeforeTheFinalTransitionWins(t *testing.T) {
	vectors := &gatedVectors{written: make(chan struct{}), release: make(chan struct{})}
	r := newRig(t, func(r *testRig) engine.Option { vectors.Store = r.Vectors; return engine.WithVectorStore(vectors) })
	col := mustTestCollection(t, r.Engine, "docs")
	vectors.armed.Store(true)
	done := make(chan error, 1)
	go func() {
		_, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, Content: versionOne})
		done <- err
	}()
	<-vectors.written
	doc := processingDoc(t, r, col.ID)
	settle()

	if res, err := r.Engine.RecoverStalled(context.Background(), 0); err != nil || res.Failed != 1 {
		t.Fatalf("RecoverStalled between the writes and ready: %+v, %v; want one failed", res, err)
	}
	close(vectors.release)
	checkInterrupted(t, r, doc.ID, <-done)
}

// revivingStore is a memory store where every document the stalled listing
// returns sends a heartbeat right after the listing, as an ingest that comes
// back to life mid-sweep would.
type revivingStore struct{ *memory.Store }

func (s revivingStore) ListStalledDocuments(ctx context.Context, before time.Time, limit int) ([]*document.Document, error) {
	docs, err := s.Store.ListStalledDocuments(ctx, before, limit)
	settle()
	for _, d := range docs {
		if touchErr := s.TouchDocument(tenantCtx(d.TenantID), d.ID, time.Time{}); touchErr != nil {
			return nil, touchErr
		}
	}
	return docs, err
}

// The guarded fail runs before anything is deleted, so an ingest that sends
// a heartbeat between the listing and that write keeps its document and
// every chunk and vector it wrote.
func TestRecoveryLeavesAnIngestThatCameBackAlone(t *testing.T) {
	hooks := &failureHooks{}
	r := newRig(t, withOpt(engine.WithExtension(hooks)),
		func(r *testRig) engine.Option { return engine.WithStore(revivingStore{r.Store}) })
	col := mustTestCollection(t, r.Engine, "docs")
	alive := plantCrashed(t, r, col, document.StateProcessing, 2)
	settle()

	res, err := r.Engine.RecoverStalled(context.Background(), 0)
	if err != nil {
		t.Fatalf("RecoverStalled: %v", err)
	}
	if res != (engine.RecoveryResult{}) {
		t.Errorf("RecoverStalled: %+v, want nothing done", res)
	}
	doc, err := r.Engine.GetDocument(tenantCtx("t1"), alive.ID)
	if err != nil || doc.State != document.StateProcessing {
		t.Fatalf("document after the sweep: %+v, %v; want still processing", doc, err)
	}
	if n, err := r.Store.CountChunks(tenantCtx("t1"), &chunk.CountFilter{DocumentID: alive.ID}); err != nil || n != 2 {
		t.Errorf("chunk rows after the sweep: %d (%v), want 2", n, err)
	}
	if v := vectorsOf(t, r.Vectors, map[string]string{"document_id": alive.ID.String()}); len(v) != 2 {
		t.Errorf("vectors after the sweep: %d, want 2", len(v))
	}
	if got := hooks.got(); len(got) != 0 {
		t.Errorf("IngestFailed hooks: %v, want none", got)
	}
}

func TestTwoSweepsRecoverADocumentOnce(t *testing.T) {
	hooks := &failureHooks{}
	r := newRig(t, withOpt(engine.WithExtension(hooks)))
	col := mustTestCollection(t, r.Engine, "docs")
	plantCrashed(t, r, col, document.StateProcessing, 2)
	settle()

	// Two replicas sweep at once.
	const sweeps = 4
	results := make([]engine.RecoveryResult, sweeps)
	var wg sync.WaitGroup
	for i := range sweeps {
		wg.Go(func() {
			res, err := r.Engine.RecoverStalled(context.Background(), 0)
			if err != nil {
				t.Errorf("sweep %d: %v", i, err)
			}
			results[i] = res
		})
	}
	wg.Wait()
	failed := 0
	for _, res := range results {
		failed += res.Failed
	}
	if failed != 1 {
		t.Errorf("sweeps: %+v, want the document counted failed once", results)
	}
	if got := hooks.got(); len(got) != 1 {
		t.Errorf("IngestFailed hooks: %d, want 1", len(got))
	}
}

func TestRecoveryLoopRunsUntilStop(t *testing.T) {
	recording := &recordingStore{}
	r := newRig(t,
		func(r *testRig) engine.Option { recording.Store = r.Store; return engine.WithStore(recording) },
		withOpt(engine.WithRecovery(engine.RecoveryConfig{Interval: 10 * time.Millisecond, StalledAfter: 0})),
	)
	col := mustTestCollection(t, r.Engine, "docs")
	stalled := plantCrashed(t, r, col, document.StateProcessing, 1)

	if err := r.Engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		doc, err := r.Store.GetDocument(tenantCtx("t1"), stalled.ID)
		if err != nil {
			t.Fatalf("get the stalled document: %v", err)
		}
		if doc.State == document.StateFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stalled document still %s after 1s of a 10ms loop", doc.State)
		}
		time.Sleep(5 * time.Millisecond)
	}

	if err := r.Engine.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	after := recording.calls()
	time.Sleep(50 * time.Millisecond)
	if n := recording.calls(); n != after {
		t.Errorf("store: %d calls after Stop returned, want none", n-after)
	}
}

func TestRecoveryConfigDefaults(t *testing.T) {
	r := newRig(t)
	if got := r.Engine.RecoveryConfig(); got != (engine.RecoveryConfig{StalledAfter: engine.DefaultStalledAfter}) {
		t.Errorf("RecoveryConfig without WithRecovery: %+v, want no loop and the default StalledAfter", got)
	}
	recording := &recordingStore{}
	r = newRig(t, func(r *testRig) engine.Option { recording.Store = r.Store; return engine.WithStore(recording) })
	if err := r.Engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := r.Engine.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n := recording.listings.Load(); n != 0 {
		t.Errorf("store: %d stalled listings with no loop configured, want 0", n)
	}
}
