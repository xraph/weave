// Package observability provides a metrics extension for Weave that records
// lifecycle event counts via go-utils MetricFactory.
package observability

import (
	"context"
	"time"

	gu "github.com/xraph/go-utils/metrics"

	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/ext"
	"github.com/xraph/weave/id"
)

// Compile-time interface checks.
var (
	_ ext.Extension          = (*MetricsExtension)(nil)
	_ ext.CollectionCreated  = (*MetricsExtension)(nil)
	_ ext.CollectionDeleted  = (*MetricsExtension)(nil)
	_ ext.IngestStarted      = (*MetricsExtension)(nil)
	_ ext.IngestCompleted    = (*MetricsExtension)(nil)
	_ ext.IngestFailed       = (*MetricsExtension)(nil)
	_ ext.RetrievalStarted   = (*MetricsExtension)(nil)
	_ ext.RetrievalCompleted = (*MetricsExtension)(nil)
	_ ext.RetrievalFailed    = (*MetricsExtension)(nil)
	_ ext.DocumentDeleted    = (*MetricsExtension)(nil)
	_ ext.ReindexStarted     = (*MetricsExtension)(nil)
	_ ext.ReindexCompleted   = (*MetricsExtension)(nil)
)

// MetricsExtension records system-wide lifecycle metrics via go-utils MetricFactory.
// Register it as a Weave extension to automatically track ingestion rates,
// retrieval counts, failure rates, and reindex operations.
type MetricsExtension struct {
	CollectionCreated  gu.Counter
	CollectionDeleted  gu.Counter
	IngestStarted      gu.Counter
	IngestCompleted    gu.Counter
	IngestFailed       gu.Counter
	DocumentsIngested  gu.Counter
	ChunksCreated      gu.Counter
	RetrievalStarted   gu.Counter
	RetrievalCompleted gu.Counter
	RetrievalFailed    gu.Counter
	DocumentDeleted    gu.Counter
	ReindexStarted     gu.Counter
	ReindexCompleted   gu.Counter
}

// NewMetricsExtension creates a MetricsExtension using a default metrics collector.
func NewMetricsExtension() *MetricsExtension {
	return NewMetricsExtensionWithFactory(gu.NewMetricsCollector("weave/observability"))
}

// NewMetricsExtensionWithFactory creates a MetricsExtension with the provided MetricFactory.
// Use fapp.Metrics() in forge extensions, or gu.NewMetricsCollector for testing.
func NewMetricsExtensionWithFactory(factory gu.MetricFactory) *MetricsExtension {
	return &MetricsExtension{
		CollectionCreated:  factory.Counter("weave.collection.created"),
		CollectionDeleted:  factory.Counter("weave.collection.deleted"),
		IngestStarted:      factory.Counter("weave.ingest.started"),
		IngestCompleted:    factory.Counter("weave.ingest.completed"),
		IngestFailed:       factory.Counter("weave.ingest.failed"),
		DocumentsIngested:  factory.Counter("weave.documents.ingested"),
		ChunksCreated:      factory.Counter("weave.chunks.created"),
		RetrievalStarted:   factory.Counter("weave.retrieval.started"),
		RetrievalCompleted: factory.Counter("weave.retrieval.completed"),
		RetrievalFailed:    factory.Counter("weave.retrieval.failed"),
		DocumentDeleted:    factory.Counter("weave.document.deleted"),
		ReindexStarted:     factory.Counter("weave.reindex.started"),
		ReindexCompleted:   factory.Counter("weave.reindex.completed"),
	}
}

// Name implements ext.Extension.
func (m *MetricsExtension) Name() string { return "observability-metrics" }

// ── Collection lifecycle hooks ──────────────────────

// OnCollectionCreated implements ext.CollectionCreated.
func (m *MetricsExtension) OnCollectionCreated(_ context.Context, _ *collection.Collection) error {
	m.CollectionCreated.Inc()
	return nil
}

// OnCollectionDeleted implements ext.CollectionDeleted.
func (m *MetricsExtension) OnCollectionDeleted(_ context.Context, _ id.CollectionID) error {
	m.CollectionDeleted.Inc()
	return nil
}

// ── Ingestion lifecycle hooks ───────────────────────

// OnIngestStarted implements ext.IngestStarted.
func (m *MetricsExtension) OnIngestStarted(_ context.Context, _ id.CollectionID, _ []*document.Document) error {
	m.IngestStarted.Inc()
	return nil
}

// OnIngestCompleted implements ext.IngestCompleted.
func (m *MetricsExtension) OnIngestCompleted(_ context.Context, _ id.CollectionID, docCount, chunkCount int, _ time.Duration) error {
	m.IngestCompleted.Inc()
	m.DocumentsIngested.Add(float64(docCount))
	m.ChunksCreated.Add(float64(chunkCount))
	return nil
}

// OnIngestFailed implements ext.IngestFailed.
func (m *MetricsExtension) OnIngestFailed(_ context.Context, _ id.CollectionID, _ error) error {
	m.IngestFailed.Inc()
	return nil
}

// ── Retrieval lifecycle hooks ───────────────────────

// OnRetrievalStarted implements ext.RetrievalStarted.
func (m *MetricsExtension) OnRetrievalStarted(_ context.Context, _ id.CollectionID, _ string) error {
	m.RetrievalStarted.Inc()
	return nil
}

// OnRetrievalCompleted implements ext.RetrievalCompleted.
func (m *MetricsExtension) OnRetrievalCompleted(_ context.Context, _ id.CollectionID, _ int, _ time.Duration) error {
	m.RetrievalCompleted.Inc()
	return nil
}

// OnRetrievalFailed implements ext.RetrievalFailed.
func (m *MetricsExtension) OnRetrievalFailed(_ context.Context, _ id.CollectionID, _ error) error {
	m.RetrievalFailed.Inc()
	return nil
}

// ── Document lifecycle hooks ────────────────────────

// OnDocumentDeleted implements ext.DocumentDeleted.
func (m *MetricsExtension) OnDocumentDeleted(_ context.Context, _ id.DocumentID) error {
	m.DocumentDeleted.Inc()
	return nil
}

// ── Reindex lifecycle hooks ─────────────────────────

// OnReindexStarted implements ext.ReindexStarted.
func (m *MetricsExtension) OnReindexStarted(_ context.Context, _ id.CollectionID) error {
	m.ReindexStarted.Inc()
	return nil
}

// OnReindexCompleted implements ext.ReindexCompleted.
func (m *MetricsExtension) OnReindexCompleted(_ context.Context, _ id.CollectionID, _ time.Duration) error {
	m.ReindexCompleted.Inc()
	return nil
}
