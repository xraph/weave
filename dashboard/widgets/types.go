package widgets

// EntityCounts holds entity count data for widget display.
type EntityCounts struct {
	Collections    int64
	Documents      int64
	DocsReady      int64
	DocsProcessing int64
	DocsFailed     int64
	DocsPending    int64
	Chunks         int64
}

// PipelineStatus holds pipeline component availability.
type PipelineStatus struct {
	HasLoader      bool
	HasChunker     bool
	HasEmbedder    bool
	HasVectorStore bool
	HasRetriever   bool
}
