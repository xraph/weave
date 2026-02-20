package assembler

import (
	"fmt"
	"strings"
)

// Template defines how retrieved chunks are formatted into context.
type Template struct {
	// Header is prepended to the assembled context.
	Header string
	// Footer is appended to the assembled context.
	Footer string
	// Separator is placed between chunks.
	Separator string
	// ChunkFormat formats each chunk. Use %d for index and %s for content.
	ChunkFormat string
}

// DefaultTemplate returns a sensible default template.
func DefaultTemplate() *Template {
	return &Template{
		Header:      "Relevant context:\n\n",
		Footer:      "",
		Separator:   "\n\n---\n\n",
		ChunkFormat: "[%d] %s",
	}
}

// Render formats the chunks using this template.
func (t *Template) Render(chunks []string) string {
	var b strings.Builder

	if t.Header != "" {
		b.WriteString(t.Header)
	}

	for i, chunk := range chunks {
		if i > 0 && t.Separator != "" {
			b.WriteString(t.Separator)
		}

		if t.ChunkFormat != "" {
			fmt.Fprintf(&b, t.ChunkFormat, i+1, chunk)
		} else {
			b.WriteString(chunk)
		}
	}

	if t.Footer != "" {
		b.WriteString(t.Footer)
	}

	return b.String()
}
