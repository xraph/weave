package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	log "github.com/xraph/go-utils/log"

	"github.com/xraph/weave"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/telemetry"
)

// DefaultStalledAfter is the StalledAfter an engine runs with when
// WithRecovery doesn't set one.
const DefaultStalledAfter = 15 * time.Minute

// RecoveryConfig sets how the engine recovers documents an ingest left
// behind when its process died: pending or processing documents that never
// finished, and superseded ones a replace never purged.
//
// StalledAfter is how long a pending or processing document may go without
// a heartbeat before it counts as stalled. An ingest sends one when it
// starts, after chunking, after embedding and right before it marks the
// document ready, so StalledAfter must stay well above the longest single
// chunk or embed step. Recovery fails a document older than that, and a
// keyed ingest of the same content claims it instead of waiting.
//
// Every zero means "the default": Interval 0 or less runs no loop,
// StalledAfter 0 or less is DefaultStalledAfter, and BatchSize 0 or less is
// 100. So RecoveryConfig{Interval: 5 * time.Minute} sweeps every 5 minutes
// with a 15 minute cutoff. A test that wants every document stalled at once
// sets StalledAfter to a nanosecond.
type RecoveryConfig struct {
	Interval     time.Duration // 0 or less: no background loop
	StalledAfter time.Duration
	BatchSize    int // per sweep; <= 0 means 100
}

// RecoveryResult counts what one RecoverStalled sweep did.
type RecoveryResult struct {
	Failed          int // pending or processing documents marked failed
	Purged          int // superseded documents removed
	SkippedNoTenant int // rows without a tenant (written before v1.9), left alone
}

// RecoveryConfig returns the recovery settings the engine runs with: the
// ones WithRecovery gave, with a StalledAfter of zero or less replaced by
// DefaultStalledAfter, or no background loop and DefaultStalledAfter.
func (e *Engine) RecoveryConfig() RecoveryConfig { return e.recovery }

// RecoverStalled runs one sweep over every tenant. Each stalled document is
// handled under its own tenant. A pending or processing row is marked failed
// with weave.ErrIngestInterrupted's message, and only then are its vectors
// and chunks deleted. A superseded row loses its vectors and chunks, then
// the row. Safe to run on every replica.
//
// A document is stalled when it was last updated more than stalledAfter
// ago; zero means anything updated before now, and a negative value counts
// as zero, so a sweep never reaches past the present. The sweep lists at most BatchSize of them, oldest first, and needs no
// tenant on ctx. Marking failed comes first and is a guarded write: it only
// succeeds while the document is still pending or processing and has sent no
// heartbeat since the cutoff. When it loses (an ingest that came back to
// life sent a heartbeat, or made the document ready), the sweep leaves the
// document and all its data alone. Of two sweeps that list one document,
// only one counts it and fires the IngestFailed hook. Rows without a tenant
// are never listed; SkippedNoTenant is their count. A document whose data
// can't be deleted, or a superseded row that fails to go, returns the first
// such error after the rest of the batch is done.
//
// This is the reverse of the order in the plan, which deleted first: that
// order could delete the data of an ingest that sent a heartbeat between the
// listing and the guarded write, and let it turn ready with chunks missing.
func (e *Engine) RecoverStalled(ctx context.Context, stalledAfter time.Duration) (RecoveryResult, error) {
	var res RecoveryResult
	if e.store == nil {
		return res, weave.ErrNoStore
	}
	ctx, span := telemetry.Tracer().Start(ctx, "recover stalled documents")
	defer span.End()

	before := time.Now().UTC().Add(-max(stalledAfter, 0))
	docs, err := e.store.ListStalledDocuments(ctx, before, e.recovery.BatchSize)
	if err != nil {
		telemetry.Fail(span, err)
		return res, err
	}

	var firstErr error
	untenanted, err := e.store.CountStalledUntenanted(ctx, before)
	if err != nil {
		firstErr = err
	}
	res.SkippedNoTenant = int(untenanted)
	for _, doc := range docs {
		if doc.TenantID == "" {
			res.SkippedNoTenant++
			continue
		}
		// Every call from here on is scoped to the document's own tenant.
		outcome, recErr := e.recoverDocument(weave.WithTenant(ctx, doc.TenantID), doc, before)
		if recErr != nil && firstErr == nil {
			firstErr = fmt.Errorf("weave: recover document %s: %w", doc.ID, recErr)
		}
		switch outcome {
		case recoveredFailed:
			res.Failed++
		case recoveredPurged:
			res.Purged++
		}
	}

	span.SetAttributes(
		telemetry.RecoverListed.Int(len(docs)),
		telemetry.RecoverFailed.Int(res.Failed),
		telemetry.RecoverPurged.Int(res.Purged),
		telemetry.RecoverSkipped.Int(res.SkippedNoTenant),
	)
	if res.SkippedNoTenant > 0 {
		// Once per sweep, not per row.
		e.logger.Warn("stalled documents without a tenant were left alone",
			log.Int("documents", res.SkippedNoTenant),
		)
	}
	if firstErr != nil {
		telemetry.Fail(span, firstErr)
		return res, firstErr
	}
	return res, nil
}

// recovered is what recoverDocument did with one document.
type recovered int

const (
	recoveredNothing recovered = iota // another sweep or a live ingest got there first
	recoveredFailed
	recoveredPurged
)

// recoverDocument recovers one stalled document. ctx carries its tenant. A
// pending or processing one is marked failed if it was still last updated
// before before, and only then loses its vectors and chunks; the outcome is
// recoveredFailed even when that delete fails, since the document is failed
// either way. A superseded one, which nothing moves on from, loses its
// vectors and chunks and then its row.
func (e *Engine) recoverDocument(ctx context.Context, doc *document.Document, before time.Time) (recovered, error) {
	if doc.State == document.StateSuperseded {
		if err := e.dropDocumentData(ctx, doc.TenantID, doc.ID); err != nil {
			return recoveredNothing, err
		}
		err := e.store.DeleteDocument(ctx, doc.ID)
		switch {
		case errors.Is(err, weave.ErrDocumentNotFound):
			return recoveredNothing, nil
		case err != nil:
			return recoveredNothing, fmt.Errorf("delete superseded document: %w", err)
		}
		return recoveredPurged, nil
	}

	err := e.store.FailDocument(ctx, doc.ID, weave.ErrIngestInterrupted.Error(), before)
	switch {
	case errors.Is(err, weave.ErrInvalidState), errors.Is(err, weave.ErrDocumentNotFound):
		return recoveredNothing, nil
	case err != nil:
		return recoveredNothing, fmt.Errorf("mark failed: %w", err)
	}
	// The document is failed now, so no ingest can make it ready. One that is
	// still running finds it failed at its next heartbeat or at the swap, and
	// deletes whatever it writes after this. The delete runs on a cleanup
	// context: no sweep lists a failed document again, so a Stop landing
	// here must not leave its chunks searchable.
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	dropErr := e.dropDocumentData(cleanup, doc.TenantID, doc.ID)
	e.extensions.EmitIngestFailed(ctx, doc.CollectionID, weave.ErrIngestInterrupted)
	return recoveredFailed, dropErr
}

// dropDocumentData deletes a document's vectors and chunks within tenant,
// and leaves its row.
func (e *Engine) dropDocumentData(ctx context.Context, tenant string, docID id.DocumentID) error {
	if e.vectorStore != nil {
		if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
			"document_id": docID.String(),
			"tenant_id":   tenant,
		}); err != nil {
			return fmt.Errorf("weave: delete vectors: %w", err)
		}
	}
	if err := e.store.DeleteChunksByDocument(ctx, docID); err != nil {
		return fmt.Errorf("weave: delete chunks: %w", err)
	}
	return nil
}

// stalledBefore is the cutoff a keyed ingest uses to decide whether an
// in-progress document of its content is stalled and may be claimed.
func (e *Engine) stalledBefore() time.Time {
	return time.Now().UTC().Add(-e.recovery.StalledAfter)
}

// heartbeat tells the store the ingest of docID is alive, and moves the
// document from pending to processing on the first beat. A document that is
// no longer pending or processing (recovery failed it, another attempt
// finished it) or is gone is weave.ErrIngestInterrupted: the ingest must
// stop and clear what it wrote.
func (e *Engine) heartbeat(ctx context.Context, docID id.DocumentID) error {
	err := e.store.TouchDocument(ctx, docID, time.Time{})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, weave.ErrInvalidState), errors.Is(err, weave.ErrDocumentNotFound):
		return weave.ErrIngestInterrupted
	}
	return fmt.Errorf("heartbeat: %w", err)
}

// startRecovery launches the recovery loop when the config asks for one and
// the engine has a store. The loop outlives ctx's cancellation, which only
// Stop ends, but keeps its values.
func (e *Engine) startRecovery(ctx context.Context) {
	e.recoveryMu.Lock()
	defer e.recoveryMu.Unlock()
	if e.recoveryStop != nil || e.recovery.Interval <= 0 || e.store == nil {
		return
	}
	loopCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	e.recoveryStop, e.recoveryDone = cancel, done
	go e.runRecovery(loopCtx, done)
}

// runRecovery sweeps every Interval until ctx ends, then closes done. A
// failed sweep is logged with its error type only, since a store error can
// quote a DSN.
func (e *Engine) runRecovery(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(e.recovery.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := e.RecoverStalled(ctx, e.recovery.StalledAfter); err != nil && ctx.Err() == nil {
				e.logger.Warn("recovery sweep failed",
					log.String("error_type", telemetry.ErrorType(err)),
				)
			}
		}
	}
}

// stopRecovery cancels the recovery loop and waits for it to return, so no
// sweep touches the store after it.
func (e *Engine) stopRecovery() {
	e.recoveryMu.Lock()
	cancel, done := e.recoveryStop, e.recoveryDone
	e.recoveryStop, e.recoveryDone = nil, nil
	e.recoveryMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
