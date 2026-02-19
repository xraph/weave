package chunker

import (
	"context"
	"strings"
)

// FixedChunker splits text into fixed-size chunks at exact character boundaries.
type FixedChunker struct{}

// NewFixedChunker creates a new FixedChunker.
func NewFixedChunker() *FixedChunker { return &FixedChunker{} }

// Chunk splits text into fixed-size chunks.
func (c *FixedChunker) Chunk(_ context.Context, text string, opts *Options) ([]ChunkResult, error) {
	chunkSize := 512
	overlap := 0
	if opts != nil {
		if opts.ChunkSize > 0 {
			chunkSize = opts.ChunkSize
		}
		if opts.ChunkOverlap > 0 {
			overlap = opts.ChunkOverlap
		}
	}

	// Use character count as a proxy for tokens (rough approximation).
	charSize := chunkSize * 4 // ~4 chars per token
	charOverlap := overlap * 4

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}

	var results []ChunkResult
	start := 0
	idx := 0

	for start < len(text) {
		end := start + charSize
		if end > len(text) {
			end = len(text)
		}

		content := text[start:end]
		results = append(results, ChunkResult{
			Content:     content,
			Index:       idx,
			StartOffset: start,
			EndOffset:   end,
			TokenCount:  len(content) / 4,
		})

		idx++
		start = end - charOverlap
		if start >= end {
			break
		}
	}

	return results, nil
}
