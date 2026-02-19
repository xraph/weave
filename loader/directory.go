package loader

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// DirectoryLoader recursively walks a directory and loads files using delegate loaders.
type DirectoryLoader struct {
	loaders []Loader
}

// NewDirectoryLoader creates a DirectoryLoader that delegates to the given loaders.
func NewDirectoryLoader(loaders ...Loader) *DirectoryLoader {
	return &DirectoryLoader{loaders: loaders}
}

// Load is not supported for DirectoryLoader — use LoadDir instead.
func (l *DirectoryLoader) Load(_ context.Context, _ io.Reader) (*LoadResult, error) {
	return nil, fmt.Errorf("weave: DirectoryLoader.Load not supported; use LoadDir")
}

// LoadDir recursively walks a directory and returns load results for each file.
func (l *DirectoryLoader) LoadDir(ctx context.Context, dirPath string) ([]*LoadResult, error) {
	var results []*LoadResult

	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		mimeType := mimeFromExt(filepath.Ext(path))
		loader := l.findLoader(mimeType)
		if loader == nil {
			return nil // Skip unsupported files.
		}

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("weave: directory load: %w", err)
		}
		defer f.Close()

		result, err := loader.Load(ctx, f)
		if err != nil {
			return fmt.Errorf("weave: directory load %s: %w", path, err)
		}

		if result.Metadata == nil {
			result.Metadata = make(map[string]string)
		}
		result.Metadata["source_path"] = path
		results = append(results, result)
		return nil
	})

	return results, err
}

// Supports always returns false — use LoadDir directly.
func (l *DirectoryLoader) Supports(_ string) bool { return false }

func (l *DirectoryLoader) findLoader(mimeType string) Loader {
	for _, loader := range l.loaders {
		if loader.Supports(mimeType) {
			return loader
		}
	}
	return nil
}

// mimeFromExt returns a MIME type for common file extensions.
func mimeFromExt(ext string) string {
	ext = strings.ToLower(ext)
	// Check Go's built-in MIME types first.
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	// Fallback for common types.
	switch ext {
	case ".md", ".markdown":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".html", ".htm":
		return "text/html"
	case ".txt":
		return "text/plain"
	default:
		return ""
	}
}
