package chunker

import (
	"context"
	"regexp"
	"strings"
)

// SemanticChunker groups text by sentence boundaries to create
// semantically coherent chunks.
type SemanticChunker struct{}

// NewSemanticChunker creates a new SemanticChunker.
func NewSemanticChunker() *SemanticChunker { return &SemanticChunker{} }

var reSentence = regexp.MustCompile(`[.!?]+\s+`)

// Chunk splits text at sentence boundaries, grouping sentences into
// chunks that don't exceed the target size.
func (c *SemanticChunker) Chunk(_ context.Context, text string, opts *Options) ([]ChunkResult, error) {
	chunkSize := 512
	if opts != nil && opts.ChunkSize > 0 {
		chunkSize = opts.ChunkSize
	}

	charSize := chunkSize * 4

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}

	// Split into sentences.
	indices := reSentence.FindAllStringIndex(text, -1)
	var sentences []string
	start := 0
	for _, idx := range indices {
		end := idx[1]
		sentences = append(sentences, strings.TrimSpace(text[start:end]))
		start = end
	}
	if start < len(text) {
		remaining := strings.TrimSpace(text[start:])
		if remaining != "" {
			sentences = append(sentences, remaining)
		}
	}

	// Group sentences into chunks.
	var results []ChunkResult
	var current strings.Builder
	currentStart := 0
	chunkIdx := 0

	for _, sentence := range sentences {
		if current.Len()+len(sentence)+1 > charSize && current.Len() > 0 {
			content := strings.TrimSpace(current.String())
			results = append(results, ChunkResult{
				Content:     content,
				Index:       chunkIdx,
				StartOffset: currentStart,
				EndOffset:   currentStart + len(content),
				TokenCount:  len(content) / 4,
			})
			chunkIdx++
			currentStart += len(content)
			current.Reset()
		}

		if current.Len() > 0 {
			current.WriteString(" ")
		}
		current.WriteString(sentence)
	}

	if current.Len() > 0 {
		content := strings.TrimSpace(current.String())
		results = append(results, ChunkResult{
			Content:     content,
			Index:       chunkIdx,
			StartOffset: currentStart,
			EndOffset:   currentStart + len(content),
			TokenCount:  len(content) / 4,
		})
	}

	return results, nil
}
