package loader

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// CSVLoader loads CSV files, joining rows into text.
type CSVLoader struct {
	// Separator between columns in output (default: " | ").
	Separator string
}

// NewCSVLoader creates a new CSVLoader.
func NewCSVLoader() *CSVLoader {
	return &CSVLoader{Separator: " | "}
}

// Load reads CSV and returns joined text content.
func (l *CSVLoader) Load(_ context.Context, reader io.Reader) (*LoadResult, error) {
	r := csv.NewReader(reader)
	r.FieldsPerRecord = -1 // Allow variable column counts.

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("weave: csv load: %w", err)
	}

	sep := l.Separator
	if sep == "" {
		sep = " | "
	}

	var b strings.Builder
	for _, row := range records {
		b.WriteString(strings.Join(row, sep))
		b.WriteString("\n")
	}

	return &LoadResult{
		Content:  strings.TrimSpace(b.String()),
		MimeType: "text/csv",
		Metadata: map[string]string{
			"row_count": fmt.Sprintf("%d", len(records)),
		},
	}, nil
}

// Supports returns true for CSV MIME types.
func (l *CSVLoader) Supports(mimeType string) bool {
	return mimeType == "text/csv"
}
