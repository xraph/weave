package engine

import (
	"fmt"

	"github.com/xraph/weave"
	"github.com/xraph/weave/loader"
)

// probeContentTypes is the list a loader's Supports is asked about. The
// Pipeline page shows the answers, never a hand-written list.
var probeContentTypes = []string{
	"text/plain", "text/markdown", "text/x-markdown", "text/html",
	"application/xhtml+xml", "text/csv", "application/json", "text/uri-list",
}

// Component is one wired pipeline stage.
type Component struct {
	weave.ComponentInfo
	Type         string   `json:"type,omitempty"`
	Configured   bool     `json:"configured"`
	ContentTypes []string `json:"content_types,omitempty"`
	Dimensions   int      `json:"dimensions,omitempty"`
}

// Components reports what the engine actually runs.
type Components struct {
	Loader      Component `json:"loader"`
	Chunker     Component `json:"chunker"`
	Embedder    Component `json:"embedder"`
	VectorStore Component `json:"vector_store"`
	// Retriever is unconfigured when the engine falls back to a plain vector
	// search.
	Retriever Component `json:"retriever"`
	// Score is what a retrieval score means on this engine.
	Score weave.ScoreKind `json:"score"`
	// TenantFilter is "verified" when the vector store's exact tenant filter
	// is known to work, otherwise "unverified".
	TenantFilter string `json:"tenant_filter"`
}

func describe(x any) Component {
	if x == nil {
		return Component{}
	}
	c := Component{Configured: true, Type: fmt.Sprintf("%T", x)}
	if d, ok := x.(weave.Describer); ok {
		c.ComponentInfo = d.Describe()
	} else {
		c.ComponentInfo = weave.ComponentInfo{Kind: "custom"}
	}
	return c
}

// Components describes every stage. A stage that is not configured reports
// Configured false, so a page can say so rather than call it active.
func (e *Engine) Components() Components {
	var c Components
	if e.loader != nil {
		c.Loader = describe(e.loader)
		c.Loader.ContentTypes = supported(e.loader)
	}
	if e.chunker != nil {
		c.Chunker = describe(e.chunker)
	}
	if e.embedder != nil {
		c.Embedder = describe(e.embedder)
		c.Embedder.Dimensions = e.embedder.Dimensions()
	}
	if e.vectorStore != nil {
		c.VectorStore = describe(e.vectorStore)
	}
	if e.retriever != nil {
		c.Retriever = describe(e.retriever)
	}

	switch {
	case c.Retriever.Configured:
		c.Score = c.Retriever.Score
	case c.VectorStore.Configured:
		c.Score = c.VectorStore.Score
	}
	if c.Score == "" {
		c.Score = weave.ScoreUnknown
	}
	c.TenantFilter = c.VectorStore.TenantFilter
	if c.TenantFilter == "" {
		c.TenantFilter = "unverified"
	}
	return c
}

func supported(l loader.Loader) []string {
	var out []string
	for _, ct := range probeContentTypes {
		if l.Supports(ct) {
			out = append(out, ct)
		}
	}
	return out
}
