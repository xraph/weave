package chunker

import (
	"context"
	"strings"
)

// SlidingChunker splits text using a sliding window with overlap.
type SlidingChunker struct{}

// NewSlidingChunker creates a new SlidingChunker.
func NewSlidingChunker() *SlidingChunker { return &SlidingChunker{} }

// Chunk splits text using a sliding window.
func (c *SlidingChunker) Chunk(_ context.Context, text string, opts *Options) ([]ChunkResult, error) {
	chunkSize := 512
	overlap := 50
	if opts != nil {
		if opts.ChunkSize > 0 {
			chunkSize = opts.ChunkSize
		}
		if opts.ChunkOverlap > 0 {
			overlap = opts.ChunkOverlap
		}
	}

	charSize := chunkSize * 4
	charOverlap := overlap * 4

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}

	var results []ChunkResult
	start := 0
	idx := 0
	step := charSize - charOverlap
	if step <= 0 {
		step = 1
	}

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
		start += step
		if start >= len(text) {
			break
		}
	}

	return results, nil
}
