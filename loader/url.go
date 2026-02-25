package loader

import (
	"context"
	"fmt"
	"net/http"
)

// URLLoader fetches content from a URL and delegates to an appropriate loader.
type URLLoader struct {
	client   *http.Client
	delegate Loader
}

// NewURLLoader creates a URLLoader that uses the given loader for content extraction.
func NewURLLoader(delegate Loader) *URLLoader {
	return &URLLoader{
		client:   http.DefaultClient,
		delegate: delegate,
	}
}

// NewURLLoaderWithClient creates a URLLoader with a custom HTTP client.
func NewURLLoaderWithClient(client *http.Client, delegate Loader) *URLLoader {
	return &URLLoader{
		client:   client,
		delegate: delegate,
	}
}

// Load fetches the URL and delegates content extraction.
func (l *URLLoader) Load(_ context.Context, _ /* unused reader */ interface{ Read([]byte) (int, error) }) (*LoadResult, error) {
	return nil, fmt.Errorf("weave: URLLoader.Load requires a URL; use LoadURL instead")
}

// LoadURL fetches a URL and extracts content using the delegate loader.
func (l *URLLoader) LoadURL(ctx context.Context, url string) (*LoadResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("weave: url load: %w", err)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("weave: url load: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("weave: url load: status %d", resp.StatusCode)
	}

	result, err := l.delegate.Load(ctx, resp.Body)
	if err != nil {
		return nil, err
	}

	if result.Metadata == nil {
		result.Metadata = make(map[string]string)
	}
	result.Metadata["source_url"] = url
	return result, nil
}

// Supports returns true for URL-based content types.
func (l *URLLoader) Supports(mimeType string) bool {
	return mimeType == "text/uri-list"
}
