package loader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// JSONLoader loads JSON documents, optionally extracting specific fields.
type JSONLoader struct {
	// Fields to extract. Empty means extract all string values.
	Fields []string
}

// NewJSONLoader creates a new JSONLoader.
func NewJSONLoader(fields ...string) *JSONLoader {
	return &JSONLoader{Fields: fields}
}

// Load reads JSON and returns extracted text content.
func (l *JSONLoader) Load(_ context.Context, reader io.Reader) (*LoadResult, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("weave: json load: %w", err)
	}

	var b strings.Builder
	if len(l.Fields) > 0 {
		l.extractFields(raw, &b)
	} else {
		l.extractAll(raw, &b)
	}

	return &LoadResult{
		Content:  strings.TrimSpace(b.String()),
		MimeType: "application/json",
	}, nil
}

// Supports returns true for JSON MIME types.
func (l *JSONLoader) Supports(mimeType string) bool {
	return mimeType == "application/json"
}

func (l *JSONLoader) extractFields(v any, b *strings.Builder) {
	switch val := v.(type) {
	case map[string]any:
		for _, field := range l.Fields {
			if fv, ok := val[field]; ok {
				b.WriteString(fmt.Sprintf("%v", fv))
				b.WriteString("\n")
			}
		}
	case []any:
		for _, item := range val {
			l.extractFields(item, b)
		}
	}
}

func (l *JSONLoader) extractAll(v any, b *strings.Builder) {
	switch val := v.(type) {
	case map[string]any:
		for _, fv := range val {
			l.extractAll(fv, b)
		}
	case []any:
		for _, item := range val {
			l.extractAll(item, b)
		}
	case string:
		b.WriteString(val)
		b.WriteString("\n")
	case float64, bool:
		b.WriteString(fmt.Sprintf("%v", val))
		b.WriteString("\n")
	}
}
