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
// keyed ingest of the same content claims it instead of waiting. Zero is
// taken as given and makes every pending or processing document stalled,
// which only tests want.
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
// ones WithRecovery gave, or no background loop and DefaultStalledAfter.
func (e *Engine) RecoveryConfig() RecoveryConfig { return e.recovery }

// RecoverStalled runs one sweep over every tenant. Each stalled document is
// handled under its own tenant: its vectors and chunks are deleted, then a
// superseded row is deleted and a pending or processing row is marked failed
// with weave.ErrIngestInterrupted's message. Safe to run on every replica.
//
// A document is stalled when it was last updated more than stalledAfter
// ago. The sweep lists at most BatchSize of them, oldest first, and needs no
// tenant on ctx. Marking failed is a guarded write: it only succeeds while
// the document is still pending or processing and has sent no heartbeat
// since the cutoff, so of two sweeps that list one document only one counts
// it and fires the IngestFailed hook, and an ingest that came back to life
// keeps its document. A document that fails to recover is left for the next
// sweep, and the first such error is returned after the rest are done.
func (e *Engine) RecoverStalled(ctx context.Context, stalledAfter time.Duration) (RecoveryResult, error) {
	var res RecoveryResult
	if e.store == nil {
		return res, weave.ErrNoStore
	}
	ctx, span := telemetry.Tracer().Start(ctx, "recover stalled documents")
	defer span.End()

	before := time.Now().UTC().Add(-stalledAfter)
	docs, err := e.store.ListStalledDocuments(ctx, before, e.recovery.BatchSize)
	if err != nil {
		telemetry.Fail(span, err)
		return res, err
	}

	var firstErr error
	for _, doc := range docs {
		if doc.TenantID == "" {
			res.SkippedNoTenant++
			continue
		}
		// Every call from here on is scoped to the document's own tenant.
		outcome, recErr := e.recoverDocument(weave.WithTenant(ctx, doc.TenantID), doc, before)
		switch {
		case recErr != nil:
			if firstErr == nil {
				firstErr = fmt.Errorf("weave: recover document %s: %w", doc.ID, recErr)
			}
		case outcome == recoveredFailed:
			res.Failed++
		case outcome == recoveredPurged:
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

// recoverDocument clears one stalled document's vectors and chunks, then
// deletes it when it is superseded or marks it failed when it is pending or
// processing, last updated before before. ctx carries the document's tenant.
func (e *Engine) recoverDocument(ctx context.Context, doc *document.Document, before time.Time) (recovered, error) {
	if err := e.dropDocumentData(ctx, doc.TenantID, doc.ID); err != nil {
		return recoveredNothing, err
	}
	if doc.State == document.StateSuperseded {
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
	e.extensions.EmitIngestFailed(ctx, doc.CollectionID, weave.ErrIngestInterrupted)
	return recoveredFailed, nil
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
