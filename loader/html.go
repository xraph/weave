package loader

import (
	"context"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// HTMLLoader extracts text from HTML documents.
type HTMLLoader struct{}

// NewHTMLLoader creates a new HTMLLoader.
func NewHTMLLoader() *HTMLLoader { return &HTMLLoader{} }

// Load reads HTML and returns extracted text content.
func (l *HTMLLoader) Load(_ context.Context, reader io.Reader) (*LoadResult, error) {
	doc, err := html.Parse(reader)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	extractText(doc, &b)

	return &LoadResult{
		Content:  strings.TrimSpace(b.String()),
		MimeType: "text/html",
	}, nil
}

// Supports returns true for HTML MIME types.
func (l *HTMLLoader) Supports(mimeType string) bool {
	return mimeType == "text/html" || mimeType == "application/xhtml+xml"
}

// extractText recursively extracts text from HTML nodes, skipping
// script and style elements.
func extractText(n *html.Node, b *strings.Builder) {
	if n.Type == html.ElementNode {
		switch n.Data {
		case "script", "style", "noscript":
			return
		}
	}

	if n.Type == html.TextNode {
		text := strings.TrimSpace(n.Data)
		if text != "" {
			b.WriteString(text)
			b.WriteString(" ")
		}
	}

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		extractText(c, b)
	}

	// Add newlines after block elements.
	if n.Type == html.ElementNode {
		switch n.Data {
		case "p", "div", "br", "h1", "h2", "h3", "h4", "h5", "h6", "li", "tr":
			b.WriteString("\n")
		}
	}
}
