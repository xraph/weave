// Package assembler builds context strings from retrieved chunks for
// use in LLM prompts, with token budgeting and citation tracking.
package assembler

import (
	"context"
	"strings"

	"github.com/xraph/weave/retriever"
)

// AssembleResult contains the assembled context and metadata.
type AssembleResult struct {
	// Context is the assembled context string for the LLM prompt.
	Context string `json:"context"`
	// Citations tracks which chunks were included.
	Citations []Citation `json:"citations,omitempty"`
	// TotalTokens is the estimated total token count.
	TotalTokens int `json:"total_tokens"`
	// TruncatedCount is the number of chunks that were dropped due to budget.
	TruncatedCount int `json:"truncated_count"`
}

// Assembler builds context strings from retrieved chunks.
type Assembler struct {
	template     *Template
	tokenCounter TokenCounter
	maxTokens    int
}

// Option configures the Assembler.
type Option func(*Assembler)

// WithTemplate sets a custom context template.
func WithTemplate(t *Template) Option {
	return func(a *Assembler) { a.template = t }
}

// WithTokenCounter sets a custom token counter.
func WithTokenCounter(tc TokenCounter) Option {
	return func(a *Assembler) { a.tokenCounter = tc }
}

// WithMaxTokens sets the maximum token budget for assembled context.
func WithMaxTokens(max int) Option {
	return func(a *Assembler) { a.maxTokens = max }
}

// New creates a new Assembler with the given options.
func New(opts ...Option) *Assembler {
	a := &Assembler{
		template:     DefaultTemplate(),
		tokenCounter: &SimpleTokenCounter{},
		maxTokens:    4096,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Assemble builds a context string from retrieved results within the token budget.
func (a *Assembler) Assemble(_ context.Context, results []retriever.Result) (*AssembleResult, error) {
	tracker := &CitationTracker{}
	budget := NewBudgetManager(a.tokenCounter, a.maxTokens)

	var included []string
	truncated := 0

	for i, r := range results {
		content := r.Chunk.Content
		tokens := budget.EstimateTokens(content)

		if !budget.CanFit(tokens) {
			truncated++
			continue
		}

		budget.Consume(tokens)
		included = append(included, content)

		tracker.Add(Citation{
			ChunkIndex: i,
			Content:    content,
			Score:      r.Score,
			Metadata:   r.Chunk.Metadata,
		})
	}

	assembled := a.template.Render(included)

	return &AssembleResult{
		Context:        assembled,
		Citations:      tracker.Citations(),
		TotalTokens:    budget.Used(),
		TruncatedCount: truncated,
	}, nil
}

// AssembleSimple creates a simple context string by joining chunk contents.
func AssembleSimple(results []retriever.Result) string {
	var parts []string
	for _, r := range results {
		parts = append(parts, r.Chunk.Content)
	}
	return strings.Join(parts, "\n\n---\n\n")
}
