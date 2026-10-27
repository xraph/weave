package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

// tick is long enough to put two writes in different milliseconds, the
// coarsest timestamp a backend keeps (Mongo's).
const tick = 15 * time.Millisecond

// testStalled pins UpdatedBefore, which the Overview uses to count
// documents stuck in processing. Three cutoffs fix the direction of the
// comparison: an inverted filter would give 2, 1, 0 instead of 0, 1, 2.
func testStalled(t *testing.T, s store.Store) {
	ctx := as("t1")
	col := mustCollection(t, s, "stalled", "t1")

	before := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	mustDocument(t, s, col, "old", document.StateProcessing)
	time.Sleep(20 * time.Millisecond)
	middle := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	mustDocument(t, s, col, "fresh", document.StateProcessing)
	mustDocument(t, s, col, "done", document.StateReady)
	time.Sleep(20 * time.Millisecond)
	after := time.Now().UTC()

	cases := []struct {
		name   string
		cutoff time.Time
		want   int64
	}{
		{"before every write", before, 0},
		{"between old and fresh", middle, 1},
		{"after every write", after, 2},
		{"zero means no filter", time.Time{}, 2},
	}
	for _, tc := range cases {
		n, err := s.CountDocuments(ctx, &document.CountFilter{State: document.StateProcessing, UpdatedBefore: tc.cutoff})
		if err != nil {
			t.Fatalf("%s: count stalled: %v", tc.name, err)
		}
		if n != tc.want {
			t.Errorf("%s: count stalled: got %d, want %d", tc.name, n, tc.want)
		}
	}
}

// testListStalledSpansTenants pins the one read with no context tenant.
// Recovery is an operator's sweep, so it lists every tenant's pending,
// processing and superseded documents older than the cutoff, oldest first,
// each with its tenant so recovery can work on it under that tenant. Ready
// and failed documents, and anything newer than the cutoff, stay out, and a
// heartbeat moves a document past the cutoff.
func testListStalledSpansTenants(t *testing.T, s store.Store) {
	c1 := mustCollection(t, s, "stalled-t1", "t1")
	c2 := mustCollection(t, s, "stalled-t2", "t2")

	stalled := make([]*document.Document, 0, 3)
	for _, w := range []struct {
		col   string
		state document.State
	}{{"t1", document.StateProcessing}, {"t2", document.StatePending}, {"t1", document.StateSuperseded}} {
		col := c1
		if w.col == "t2" {
			col = c2
		}
		stalled = append(stalled, mustDocument(t, s, col, "stalled "+string(w.state), w.state))
		time.Sleep(tick)
	}
	cutoff := time.Now().UTC()
	time.Sleep(tick)
	mustDocument(t, s, c1, "fresh processing", document.StateProcessing)
	mustDocument(t, s, c2, "ready", document.StateReady)
	mustDocument(t, s, c1, "failed", document.StateFailed)

	got, err := s.ListStalledDocuments(context.Background(), cutoff, 0)
	if err != nil {
		t.Fatalf("list stalled with no tenant: %v", err)
	}
	sameOrder(t, "stalled documents", documentIDs(got), documentIDs(stalled))
	for i, d := range got {
		if i < len(stalled) && (d.TenantID != stalled[i].TenantID || d.State != stalled[i].State) {
			t.Errorf("stalled %s: tenant %q state %q, want %q %q", d.ID, d.TenantID, d.State, stalled[i].TenantID, stalled[i].State)
		}
	}
	if got, err = s.ListStalledDocuments(context.Background(), cutoff, 2); err != nil {
		t.Fatalf("list stalled, limit 2: %v", err)
	}
	sameOrder(t, "stalled documents, limit 2", documentIDs(got), documentIDs(stalled[:2]))

	// A heartbeat on the oldest moves it past the cutoff.
	if err = s.TouchDocument(as("t1"), stalled[0].ID, time.Time{}); err != nil {
		t.Fatalf("touch the oldest stalled document: %v", err)
	}
	if got, err = s.ListStalledDocuments(context.Background(), cutoff, 0); err != nil {
		t.Fatalf("list stalled after a heartbeat: %v", err)
	}
	sameOrder(t, "stalled documents after a heartbeat", documentIDs(got), documentIDs(stalled[1:]))
}

// mustGet reads docID back as tenant.
func mustGet(t *testing.T, s store.Store, tenant string, docID id.DocumentID) *document.Document {
	t.Helper()
	got, err := s.GetDocument(as(tenant), docID)
	if err != nil {
		t.Fatalf("get document %s as %s: %v", docID, tenant, err)
	}
	return got
}

// testTouchDocument pins the heartbeat: a pending or processing document
// becomes processing with a newer UpdatedAt and every other field kept, any
// other state is ErrInvalidState and untouched, and another tenant's or an
// unknown document is ErrDocumentNotFound. With staleBefore it is a claim:
// it succeeds only on a document last updated before staleBefore, so the
// second of two claims with the same cutoff fails.
func testTouchDocument(t *testing.T, s store.Store) {
	t1 := as("t1")
	col := mustCollection(t, s, "touch", "t1")
	processing := mustDocument(t, s, col, "processing", document.StateProcessing)
	pending := mustDocument(t, s, col, "pending", document.StatePending)
	settled := []*document.Document{
		mustDocument(t, s, col, "ready", document.StateReady),
		mustDocument(t, s, col, "failed", document.StateFailed),
		mustDocument(t, s, col, "superseded", document.StateSuperseded),
	}
	other := mustDocument(t, s, mustCollection(t, s, "touch-t2", "t2"), "t2 processing", document.StateProcessing)

	before := mustGet(t, s, "t1", processing.ID)
	time.Sleep(tick)
	for _, d := range []*document.Document{processing, pending} {
		if err := s.TouchDocument(t1, d.ID, time.Time{}); err != nil {
			t.Fatalf("touch %s document: %v", d.State, err)
		}
		got := mustGet(t, s, "t1", d.ID)
		if got.State != document.StateProcessing {
			t.Errorf("touched %s document: state %q, want processing", d.State, got.State)
		}
		if got.Title != d.Title || got.ContentHash != d.ContentHash || got.Metadata["lang"] != "en" {
			t.Errorf("touched %s document lost fields: %+v", d.State, got)
		}
	}
	if got := mustGet(t, s, "t1", processing.ID); !got.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("heartbeat: UpdatedAt %v, want after %v", got.UpdatedAt, before.UpdatedAt)
	}

	for _, d := range settled {
		was := mustGet(t, s, "t1", d.ID)
		if err := s.TouchDocument(t1, d.ID, time.Time{}); !errors.Is(err, weave.ErrInvalidState) {
			t.Errorf("touch a %s document: got %v, want ErrInvalidState", d.State, err)
		}
		got := mustGet(t, s, "t1", d.ID)
		if got.State != d.State || !got.UpdatedAt.Equal(was.UpdatedAt) {
			t.Errorf("refused touch changed a %s document: state %q, updated %v (was %v)", d.State, got.State, got.UpdatedAt, was.UpdatedAt)
		}
	}
	otherWas := mustGet(t, s, "t2", other.ID)
	for name, docID := range map[string]id.DocumentID{"t2's document": other.ID, "an unknown document": id.NewDocumentID()} {
		if err := s.TouchDocument(t1, docID, time.Time{}); !errors.Is(err, weave.ErrDocumentNotFound) {
			t.Errorf("touch %s as t1: got %v, want ErrDocumentNotFound", name, err)
		}
	}
	if got := mustGet(t, s, "t2", other.ID); !got.UpdatedAt.Equal(otherWas.UpdatedAt) {
		t.Errorf("t1's touch moved t2's document: updated %v, was %v", got.UpdatedAt, otherWas.UpdatedAt)
	}

	// A claim needs a document older than its cutoff.
	early := time.Now().UTC()
	time.Sleep(tick)
	claimed := mustDocument(t, s, col, "claimed", document.StateProcessing)
	if err := s.TouchDocument(t1, claimed.ID, early); !errors.Is(err, weave.ErrInvalidState) {
		t.Errorf("claim a document newer than the cutoff: got %v, want ErrInvalidState", err)
	}
	time.Sleep(tick)
	cutoff := time.Now().UTC()
	time.Sleep(tick)
	if err := s.TouchDocument(t1, claimed.ID, cutoff); err != nil {
		t.Fatalf("claim a stalled document: %v", err)
	}
	if err := s.TouchDocument(t1, claimed.ID, cutoff); !errors.Is(err, weave.ErrInvalidState) {
		t.Errorf("claim it again with the same cutoff: got %v, want ErrInvalidState", err)
	}
}

// testFailDocument pins the guarded move to failed: a pending or processing
// document becomes failed with the error text and every other field kept,
// while a ready one (an attempt that just won ReplaceSourceDocument
// included), a failed or a superseded one is ErrInvalidState and keeps its
// state, error and chunks. With staleBefore, a document that sent a
// heartbeat after the cutoff is left processing.
func testFailDocument(t *testing.T, s store.Store) {
	t1 := as("t1")
	col := mustCollection(t, s, "fail", "t1")
	const reason = "weave: ingest interrupted before it finished"

	for _, state := range []document.State{document.StateProcessing, document.StatePending} {
		d := mustDocument(t, s, col, "fail "+string(state), state)
		if err := s.FailDocument(t1, d.ID, reason, time.Time{}); err != nil {
			t.Fatalf("fail a %s document: %v", state, err)
		}
		got := mustGet(t, s, "t1", d.ID)
		if got.State != document.StateFailed || got.Error != reason {
			t.Errorf("failed %s document: state %q error %q, want failed %q", state, got.State, got.Error, reason)
		}
		if got.Title != d.Title || got.ContentHash != d.ContentHash || got.Metadata["lang"] != "en" {
			t.Errorf("failed %s document lost fields: %+v", state, got)
		}
	}

	// A version another attempt has just made ready is never overwritten.
	won := mustSourceDocument(t, s, col, "docs/won.md", document.StateProcessing)
	wonChunks := chunksFor(won, 2)
	mustReplace(t, s, won, wonChunks)
	ready := mustDocument(t, s, col, "ready", document.StateReady)
	readyChunks := mustChunks(t, s, ready, 2)
	failed := mustDocument(t, s, col, "failed", document.StateFailed)
	superseded := mustDocument(t, s, col, "superseded", document.StateSuperseded)
	for _, d := range []*document.Document{won, ready, failed, superseded} {
		was := mustGet(t, s, "t1", d.ID)
		if err := s.FailDocument(t1, d.ID, reason, time.Time{}); !errors.Is(err, weave.ErrInvalidState) {
			t.Errorf("fail a %s document: got %v, want ErrInvalidState", was.State, err)
		}
		got := mustGet(t, s, "t1", d.ID)
		if got.State != was.State || got.Error != was.Error {
			t.Errorf("refused fail changed a %s document: state %q error %q", was.State, got.State, got.Error)
		}
	}
	assertDocument(t, s, "t1", won.ID, document.StateReady, wonChunks)
	chs, listErr := s.ListChunksByDocument(t1, ready.ID)
	if listErr != nil {
		t.Fatalf("list chunks of the ready document: %v", listErr)
	}
	sameSet(t, "chunks of the ready document", chunkIDs(chs), chunkIDs(readyChunks))

	other := mustDocument(t, s, mustCollection(t, s, "fail-t2", "t2"), "t2 processing", document.StateProcessing)
	for name, docID := range map[string]id.DocumentID{"t2's document": other.ID, "an unknown document": id.NewDocumentID()} {
		if err := s.FailDocument(t1, docID, reason, time.Time{}); !errors.Is(err, weave.ErrDocumentNotFound) {
			t.Errorf("fail %s as t1: got %v, want ErrDocumentNotFound", name, err)
		}
	}
	if got := mustGet(t, s, "t2", other.ID); got.State != document.StateProcessing || got.Error != "" {
		t.Errorf("t1's fail reached t2's document: state %q error %q", got.State, got.Error)
	}

	// With a cutoff, a heartbeat after it keeps the document processing.
	cutoff := time.Now().UTC()
	time.Sleep(tick)
	alive := mustDocument(t, s, col, "alive", document.StateProcessing)
	if err := s.FailDocument(t1, alive.ID, reason, cutoff); !errors.Is(err, weave.ErrInvalidState) {
		t.Errorf("fail a document with a heartbeat after the cutoff: got %v, want ErrInvalidState", err)
	}
	if got := mustGet(t, s, "t1", alive.ID); got.State != document.StateProcessing {
		t.Errorf("document with a fresh heartbeat: state %q, want processing", got.State)
	}
	time.Sleep(tick)
	if err := s.FailDocument(t1, alive.ID, reason, time.Now().UTC()); err != nil {
		t.Errorf("fail it with a later cutoff: %v", err)
	}
}

// testClaimOnce runs several claims of one stalled document at once, as
// replicas or recovery sweeps would: exactly one TouchDocument and, on a
// second stalled document, exactly one FailDocument succeeds, and the
// others are ErrInvalidState.
func testClaimOnce(t *testing.T, s store.Store) {
	col := mustCollection(t, s, "claim-once", "t1")
	touched := mustDocument(t, s, col, "claimed", document.StateProcessing)
	failed := mustDocument(t, s, col, "recovered", document.StateProcessing)
	time.Sleep(tick)
	cutoff := time.Now().UTC()

	const n = 8
	for name, call := range map[string]func(ctx context.Context) error{
		"TouchDocument": func(ctx context.Context) error { return s.TouchDocument(ctx, touched.ID, cutoff) },
		"FailDocument": func(ctx context.Context) error {
			return s.FailDocument(ctx, failed.ID, "weave: ingest interrupted before it finished", cutoff)
		},
	} {
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() { errs[i] = call(as("t1")) })
		}
		wg.Wait()
		won := 0
		for _, err := range errs {
			switch {
			case err == nil:
				won++
			case !errors.Is(err, weave.ErrInvalidState):
				t.Errorf("%s: a losing claim got %v, want ErrInvalidState", name, err)
			}
		}
		if won != 1 {
			t.Errorf("%s: %d of %d concurrent claims succeeded, want 1", name, won, n)
		}
	}
}

// testStalledSkipsUntenanted pins that rows with an empty tenant never reach
// recovery's listing, so however many a v1.8 install left stalled, they
// can't fill a batch: the oldest stalled rows are tenantless, and a limit of
// one still returns the tenant's row. CountStalledUntenanted counts exactly
// the tenantless rows the listing leaves out, by the same state and age
// rules.
func testStalledSkipsUntenanted(t *testing.T, s store.Store, plant Planter) {
	col := mustCollection(t, s, "legacy", "t1")
	legacy := func(state document.State) {
		now := time.Now().UTC()
		doc := &document.Document{
			Entity: weave.Entity{CreatedAt: now, UpdatedAt: now},
			ID:     id.NewDocumentID(), CollectionID: col.ID, Title: "legacy " + string(state),
			ContentHash: "sha-" + id.NewDocumentID().String(), Metadata: map[string]string{}, State: state,
		}
		plant(t, s, doc)
	}
	for _, state := range []document.State{document.StateProcessing, document.StatePending, document.StateSuperseded, document.StateProcessing} {
		legacy(state)
	}
	legacy(document.StateReady)
	legacy(document.StateFailed)
	time.Sleep(tick)
	tenanted := mustDocument(t, s, col, "t1 processing", document.StateProcessing)
	time.Sleep(tick)
	cutoff := time.Now().UTC()
	time.Sleep(tick)
	legacy(document.StateProcessing) // newer than the cutoff

	for _, limit := range []int{1, 0} {
		got, err := s.ListStalledDocuments(context.Background(), cutoff, limit)
		if err != nil {
			t.Fatalf("list stalled, limit %d: %v", limit, err)
		}
		sameOrder(t, fmt.Sprintf("stalled documents, limit %d", limit), documentIDs(got), documentIDs([]*document.Document{tenanted}))
	}
	n, err := s.CountStalledUntenanted(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("count stalled without a tenant: %v", err)
	}
	if n != 4 {
		t.Errorf("stalled without a tenant: %d, want 4 (two processing, one pending, one superseded)", n)
	}
}
