package assembler

// Citation tracks the provenance of a chunk included in the assembled context.
type Citation struct {
	// ChunkIndex is the position in the original retrieval results.
	ChunkIndex int `json:"chunk_index"`
	// Content is the chunk text.
	Content string `json:"content"`
	// Score is the retrieval relevance score.
	Score float64 `json:"score"`
	// Metadata from the source chunk.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CitationTracker collects citations as chunks are included.
type CitationTracker struct {
	citations []Citation
}

// Add records a citation.
func (t *CitationTracker) Add(c Citation) {
	t.citations = append(t.citations, c)
}

// Citations returns all recorded citations.
func (t *CitationTracker) Citations() []Citation {
	return t.citations
}

// Count returns the number of citations recorded.
func (t *CitationTracker) Count() int {
	return len(t.citations)
}
