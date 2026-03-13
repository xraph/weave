package pages

// EntityCounts holds entity count data for dashboard display.
type EntityCounts struct {
	Collections    int64
	Documents      int64
	DocsReady      int64
	DocsProcessing int64
	DocsFailed     int64
	DocsPending    int64
	Chunks         int64
}

// PipelineInfo holds pipeline component availability for display.
// This mirrors dashboard.PipelineStatus to avoid circular imports.
type PipelineInfo struct {
	HasLoader      bool
	HasChunker     bool
	HasEmbedder    bool
	HasVectorStore bool
	HasRetriever   bool
}
