package chunker

import (
	"context"
	"strings"
)

// CodeChunker splits code at function and block boundaries.
type CodeChunker struct{}

// NewCodeChunker creates a new CodeChunker.
func NewCodeChunker() *CodeChunker { return &CodeChunker{} }

// Chunk splits code text at function/block boundaries.
func (c *CodeChunker) Chunk(_ context.Context, text string, opts *Options) ([]ChunkResult, error) {
	chunkSize := 512
	if opts != nil && opts.ChunkSize > 0 {
		chunkSize = opts.ChunkSize
	}

	charSize := chunkSize * 4

	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}

	// Split on common code block boundaries.
	separators := []string{
		"\nfunc ", "\ndef ", "\nclass ",
		"\nfunction ", "\nconst ", "\nvar ", "\ntype ",
		"\n\n", "\n",
	}

	blocks := splitByAny(text, separators)

	var results []ChunkResult
	var current strings.Builder
	currentStart := 0
	chunkIdx := 0

	for _, block := range blocks {
		block = strings.TrimRight(block, " \t")
		if block == "" {
			continue
		}

		if current.Len()+len(block) > charSize && current.Len() > 0 {
			content := current.String()
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

		current.WriteString(block)
	}

	if current.Len() > 0 {
		content := current.String()
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

// splitByAny splits text by the first matching separator from the list.
func splitByAny(text string, separators []string) []string {
	for _, sep := range separators {
		if strings.Contains(text, sep) {
			parts := strings.Split(text, sep)
			var result []string
			for i, part := range parts {
				if i > 0 {
					part = sep + part // Preserve the separator as prefix.
				}
				if strings.TrimSpace(part) != "" {
					result = append(result, part)
				}
			}
			if len(result) > 1 {
				return result
			}
		}
	}
	return []string{text}
}
