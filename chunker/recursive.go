package chunker

import (
	"context"
	"strings"
)

// RecursiveChunker splits text using hierarchical separators, trying
// the largest separator first and falling back to smaller ones.
type RecursiveChunker struct {
	// Separators in priority order (largest to smallest).
	Separators []string
}

// NewRecursiveChunker creates a new RecursiveChunker with default separators.
func NewRecursiveChunker() *RecursiveChunker {
	return &RecursiveChunker{
		Separators: []string{"\n\n", "\n", ". ", " "},
	}
}

// Chunk splits text recursively using hierarchical separators.
func (c *RecursiveChunker) Chunk(_ context.Context, text string, opts *Options) ([]ChunkResult, error) {
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

	charSize := chunkSize * 4
	charOverlap := overlap * 4

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}

	chunks := c.splitRecursive(text, c.Separators, charSize, charOverlap)

	results := make([]ChunkResult, len(chunks))
	offset := 0
	for i, ch := range chunks {
		start := strings.Index(text[offset:], ch)
		if start == -1 {
			start = 0
		} else {
			start += offset
		}
		end := start + len(ch)

		results[i] = ChunkResult{
			Content:     ch,
			Index:       i,
			StartOffset: start,
			EndOffset:   end,
			TokenCount:  len(ch) / 4,
		}
		offset = start
	}

	return results, nil
}

func (c *RecursiveChunker) splitRecursive(text string, separators []string, chunkSize, overlap int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}

	if len(separators) == 0 {
		// Last resort: split at chunkSize boundaries.
		return c.splitAtSize(text, chunkSize, overlap)
	}

	sep := separators[0]
	parts := strings.Split(text, sep)

	var chunks []string
	var current strings.Builder

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if current.Len()+len(part)+len(sep) > chunkSize && current.Len() > 0 {
			chunk := strings.TrimSpace(current.String())
			if chunk != "" {
				if len(chunk) > chunkSize {
					// Recursively split with next separator.
					sub := c.splitRecursive(chunk, separators[1:], chunkSize, overlap)
					chunks = append(chunks, sub...)
				} else {
					chunks = append(chunks, chunk)
				}
			}
			current.Reset()
			// Add overlap from previous chunk.
			if overlap > 0 && len(chunk) > overlap {
				current.WriteString(chunk[len(chunk)-overlap:])
				current.WriteString(sep)
			}
		}

		if current.Len() > 0 {
			current.WriteString(sep)
		}
		current.WriteString(part)
	}

	if current.Len() > 0 {
		chunk := strings.TrimSpace(current.String())
		if chunk != "" {
			if len(chunk) > chunkSize {
				sub := c.splitRecursive(chunk, separators[1:], chunkSize, overlap)
				chunks = append(chunks, sub...)
			} else {
				chunks = append(chunks, chunk)
			}
		}
	}

	return chunks
}

func (c *RecursiveChunker) splitAtSize(text string, chunkSize, overlap int) []string {
	var chunks []string
	start := 0
	for start < len(text) {
		end := start + chunkSize
		if end > len(text) {
			end = len(text)
		}
		chunks = append(chunks, text[start:end])
		start = end - overlap
		if start >= end {
			break
		}
	}
	return chunks
}
