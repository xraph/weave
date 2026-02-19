// Package loader defines the interface for extracting text from various
// document formats.
package loader

import (
	"context"
	"io"
)

// LoadResult contains the extracted text and metadata from a loaded document.
type LoadResult struct {
	// Content is the extracted text content.
	Content string `json:"content"`
	// Metadata holds format-specific metadata (e.g. title, author, page count).
	Metadata map[string]string `json:"metadata,omitempty"`
	// MimeType is the detected MIME type of the source document.
	MimeType string `json:"mime_type,omitempty"`
}

// Loader extracts text content from a document source.
type Loader interface {
	// Load reads from the given source and returns the extracted text.
	Load(ctx context.Context, reader io.Reader) (*LoadResult, error)

	// Supports returns true if this loader can handle the given MIME type.
	Supports(mimeType string) bool
}
