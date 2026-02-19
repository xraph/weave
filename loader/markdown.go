package loader

import (
	"context"
	"io"
	"regexp"
	"strings"
)

// MarkdownLoader strips Markdown formatting and returns plain text.
type MarkdownLoader struct{}

// NewMarkdownLoader creates a new MarkdownLoader.
func NewMarkdownLoader() *MarkdownLoader { return &MarkdownLoader{} }

var (
	reHeaders    = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	reBoldItalic = regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`)
	reLinks      = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	reImages     = regexp.MustCompile(`!\[([^\]]*)\]\([^)]+\)`)
	reCode       = regexp.MustCompile("`{1,3}[^`]*`{1,3}")
	reCodeBlock  = regexp.MustCompile("(?s)```[^`]*```")
	reHR         = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
	reListMarker = regexp.MustCompile(`(?m)^[\s]*[-*+]\s+`)
	reNumList    = regexp.MustCompile(`(?m)^[\s]*\d+\.\s+`)
)

// Load reads Markdown and returns plain text.
func (l *MarkdownLoader) Load(_ context.Context, reader io.Reader) (*LoadResult, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	text := string(data)
	text = reCodeBlock.ReplaceAllString(text, "")
	text = reCode.ReplaceAllString(text, "")
	text = reImages.ReplaceAllString(text, "$1")
	text = reLinks.ReplaceAllString(text, "$1")
	text = reHeaders.ReplaceAllString(text, "")
	text = reBoldItalic.ReplaceAllString(text, "$1")
	text = reHR.ReplaceAllString(text, "")
	text = reListMarker.ReplaceAllString(text, "")
	text = reNumList.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)

	return &LoadResult{
		Content:  text,
		MimeType: "text/markdown",
	}, nil
}

// Supports returns true for Markdown MIME types.
func (l *MarkdownLoader) Supports(mimeType string) bool {
	return mimeType == "text/markdown" || mimeType == "text/x-markdown"
}
