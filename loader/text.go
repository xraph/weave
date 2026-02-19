package loader

import (
	"context"
	"io"
)

// TextLoader loads plain text content.
type TextLoader struct{}

// NewTextLoader creates a new TextLoader.
func NewTextLoader() *TextLoader { return &TextLoader{} }

// Load reads plain text from the reader.
func (l *TextLoader) Load(_ context.Context, reader io.Reader) (*LoadResult, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return &LoadResult{
		Content:  string(data),
		MimeType: "text/plain",
	}, nil
}

// Supports returns true for text MIME types.
func (l *TextLoader) Supports(mimeType string) bool {
	return mimeType == "text/plain" || mimeType == ""
}
