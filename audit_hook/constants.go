package audithook

// Severity constants (mirror chronicle/audit).
const (
	SeverityInfo     = "info"
	SeverityWarning  = "warning"
	SeverityCritical = "critical"
)

// Outcome constants (mirror chronicle/audit).
const (
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
)

// Action constants.
const (
	ActionCollectionCreated  = "weave.collection.created"
	ActionCollectionDeleted  = "weave.collection.deleted"
	ActionIngestStarted      = "weave.ingest.started"
	ActionIngestCompleted    = "weave.ingest.completed"
	ActionIngestFailed       = "weave.ingest.failed"
	ActionRetrievalStarted   = "weave.retrieval.started"
	ActionRetrievalCompleted = "weave.retrieval.completed"
	ActionRetrievalFailed    = "weave.retrieval.failed"
	ActionDocumentDeleted    = "weave.document.deleted"
	ActionReindexStarted     = "weave.reindex.started"
	ActionReindexCompleted   = "weave.reindex.completed"
)

// Resource constants.
const (
	ResourceCollection = "collection"
	ResourceDocument   = "document"
	ResourceRetrieval  = "retrieval"
	ResourceReindex    = "reindex"
)

// Category constants.
const (
	CategoryCollection = "collection"
	CategoryIngestion  = "ingestion"
	CategoryRetrieval  = "retrieval"
	CategoryReindex    = "reindex"
)
