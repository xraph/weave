// Package audithook bridges Weave lifecycle events to an audit trail backend.
//
// It defines a local Recorder interface so the package does not import
// Chronicle directly. Callers inject a RecorderFunc adapter that bridges
// to Chronicle at wiring time.
package audithook

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/ext"
	"github.com/xraph/weave/id"
)

// Compile-time interface checks.
var (
	_ ext.Extension          = (*Extension)(nil)
	_ ext.CollectionCreated  = (*Extension)(nil)
	_ ext.CollectionDeleted  = (*Extension)(nil)
	_ ext.IngestStarted      = (*Extension)(nil)
	_ ext.IngestCompleted    = (*Extension)(nil)
	_ ext.IngestFailed       = (*Extension)(nil)
	_ ext.RetrievalStarted   = (*Extension)(nil)
	_ ext.RetrievalCompleted = (*Extension)(nil)
	_ ext.RetrievalFailed    = (*Extension)(nil)
	_ ext.DocumentDeleted    = (*Extension)(nil)
	_ ext.ReindexStarted     = (*Extension)(nil)
	_ ext.ReindexCompleted   = (*Extension)(nil)
)

// Recorder is the interface that audit backends must implement.
// This matches chronicle.Emitter but is defined locally so that the
// audit_hook package does not import Chronicle directly — callers inject
// the concrete *chronicle.Chronicle at wiring time.
type Recorder interface {
	Record(ctx context.Context, event *AuditEvent) error
}

// AuditEvent is a local representation of an audit event.
// It mirrors chronicle/audit.Event but avoids a module dependency.
type AuditEvent struct {
	Action     string         `json:"action"`
	Resource   string         `json:"resource"`
	Category   string         `json:"category"`
	ResourceID string         `json:"resource_id,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	Outcome    string         `json:"outcome"`
	Severity   string         `json:"severity"`
	Reason     string         `json:"reason,omitempty"`
}

// RecorderFunc is an adapter to use a plain function as a Recorder.
type RecorderFunc func(ctx context.Context, event *AuditEvent) error

// Record implements Recorder.
func (f RecorderFunc) Record(ctx context.Context, event *AuditEvent) error {
	return f(ctx, event)
}

// Extension bridges Weave lifecycle events to an audit trail backend.
type Extension struct {
	recorder Recorder
	enabled  map[string]bool // nil = all enabled
	logger   *slog.Logger
}

// New creates an Extension that emits audit events through the provided Recorder.
func New(r Recorder, opts ...Option) *Extension {
	e := &Extension{
		recorder: r,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Name implements ext.Extension.
func (e *Extension) Name() string { return "audit-hook" }

// ── Collection lifecycle hooks ──────────────────────

// OnCollectionCreated implements ext.CollectionCreated.
func (e *Extension) OnCollectionCreated(ctx context.Context, col *collection.Collection) error {
	return e.record(ctx, ActionCollectionCreated, SeverityInfo, OutcomeSuccess,
		ResourceCollection, col.ID.String(), CategoryCollection, nil,
		"collection_name", col.Name,
	)
}

// OnCollectionDeleted implements ext.CollectionDeleted.
func (e *Extension) OnCollectionDeleted(ctx context.Context, colID id.CollectionID) error {
	return e.record(ctx, ActionCollectionDeleted, SeverityInfo, OutcomeSuccess,
		ResourceCollection, colID.String(), CategoryCollection, nil,
	)
}

// ── Ingestion lifecycle hooks ───────────────────────

// OnIngestStarted implements ext.IngestStarted.
func (e *Extension) OnIngestStarted(ctx context.Context, colID id.CollectionID, docs []*document.Document) error {
	return e.record(ctx, ActionIngestStarted, SeverityInfo, OutcomeSuccess,
		ResourceCollection, colID.String(), CategoryIngestion, nil,
		"document_count", len(docs),
	)
}

// OnIngestCompleted implements ext.IngestCompleted.
func (e *Extension) OnIngestCompleted(ctx context.Context, colID id.CollectionID, docCount, chunkCount int, elapsed time.Duration) error {
	return e.record(ctx, ActionIngestCompleted, SeverityInfo, OutcomeSuccess,
		ResourceCollection, colID.String(), CategoryIngestion, nil,
		"document_count", docCount,
		"chunk_count", chunkCount,
		"elapsed_ms", elapsed.Milliseconds(),
	)
}

// OnIngestFailed implements ext.IngestFailed.
func (e *Extension) OnIngestFailed(ctx context.Context, colID id.CollectionID, ingestErr error) error {
	return e.record(ctx, ActionIngestFailed, SeverityCritical, OutcomeFailure,
		ResourceCollection, colID.String(), CategoryIngestion, ingestErr,
	)
}

// ── Retrieval lifecycle hooks ───────────────────────

// OnRetrievalStarted implements ext.RetrievalStarted.
func (e *Extension) OnRetrievalStarted(ctx context.Context, colID id.CollectionID, query string) error {
	return e.record(ctx, ActionRetrievalStarted, SeverityInfo, OutcomeSuccess,
		ResourceRetrieval, colID.String(), CategoryRetrieval, nil,
		"query_length", len(query),
	)
}

// OnRetrievalCompleted implements ext.RetrievalCompleted.
func (e *Extension) OnRetrievalCompleted(ctx context.Context, colID id.CollectionID, resultCount int, elapsed time.Duration) error {
	return e.record(ctx, ActionRetrievalCompleted, SeverityInfo, OutcomeSuccess,
		ResourceRetrieval, colID.String(), CategoryRetrieval, nil,
		"result_count", resultCount,
		"elapsed_ms", elapsed.Milliseconds(),
	)
}

// OnRetrievalFailed implements ext.RetrievalFailed.
func (e *Extension) OnRetrievalFailed(ctx context.Context, colID id.CollectionID, retrievalErr error) error {
	return e.record(ctx, ActionRetrievalFailed, SeverityCritical, OutcomeFailure,
		ResourceRetrieval, colID.String(), CategoryRetrieval, retrievalErr,
	)
}

// ── Document lifecycle hooks ────────────────────────

// OnDocumentDeleted implements ext.DocumentDeleted.
func (e *Extension) OnDocumentDeleted(ctx context.Context, docID id.DocumentID) error {
	return e.record(ctx, ActionDocumentDeleted, SeverityInfo, OutcomeSuccess,
		ResourceDocument, docID.String(), CategoryIngestion, nil,
	)
}

// ── Reindex lifecycle hooks ─────────────────────────

// OnReindexStarted implements ext.ReindexStarted.
func (e *Extension) OnReindexStarted(ctx context.Context, colID id.CollectionID) error {
	return e.record(ctx, ActionReindexStarted, SeverityInfo, OutcomeSuccess,
		ResourceReindex, colID.String(), CategoryReindex, nil,
	)
}

// OnReindexCompleted implements ext.ReindexCompleted.
func (e *Extension) OnReindexCompleted(ctx context.Context, colID id.CollectionID, elapsed time.Duration) error {
	return e.record(ctx, ActionReindexCompleted, SeverityInfo, OutcomeSuccess,
		ResourceReindex, colID.String(), CategoryReindex, nil,
		"elapsed_ms", elapsed.Milliseconds(),
	)
}

// ── Internal helpers ────────────────────────────────

// record builds and sends an audit event if the action is enabled.
func (e *Extension) record(
	ctx context.Context,
	action, severity, outcome string,
	resource, resourceID, category string,
	err error,
	kvPairs ...any,
) error {
	if e.enabled != nil && !e.enabled[action] {
		return nil
	}

	meta := make(map[string]any, len(kvPairs)/2+1)
	for i := 0; i+1 < len(kvPairs); i += 2 {
		key, ok := kvPairs[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", kvPairs[i])
		}
		meta[key] = kvPairs[i+1]
	}

	var reason string
	if err != nil {
		reason = err.Error()
		meta["error"] = err.Error()
	}

	evt := &AuditEvent{
		Action:     action,
		Resource:   resource,
		Category:   category,
		ResourceID: resourceID,
		Metadata:   meta,
		Outcome:    outcome,
		Severity:   severity,
		Reason:     reason,
	}

	if recErr := e.recorder.Record(ctx, evt); recErr != nil {
		e.logger.Warn("audit_hook: failed to record audit event",
			"action", action,
			"resource_id", resourceID,
			"error", recErr,
		)
	}
	return nil
}
