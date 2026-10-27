// Package memory provides an in-memory vector store using brute-force
// cosine similarity. Suitable for testing and development.
package memory

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/xraph/weave/internal/tenancy"
	"github.com/xraph/weave/vectorstore"
)

// Compile-time interface check.
var _ vectorstore.VectorStore = (*Store)(nil)

// Store is an in-memory vector store with brute-force cosine similarity search.
type Store struct {
	mu      sync.RWMutex
	entries map[string]vectorstore.Entry
}

// New creates a new in-memory vector store.
func New() *Store {
	return &Store{
		entries: make(map[string]vectorstore.Entry),
	}
}

// Len returns how many entries the store holds, across every tenant.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// Upsert inserts or updates vector entries. Every entry needs a
// metadata["tenant_id"]; one without it is ErrTenantRequired, and nothing
// in the batch is written.
func (s *Store) Upsert(_ context.Context, entries []vectorstore.Entry) error {
	if err := tenancy.VectorEntries(entries); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range entries {
		s.entries[e.ID] = e
	}
	return nil
}

// Search returns the most similar entries of one tenant to the given vector
// using cosine similarity. An empty TenantKey is ErrTenantRequired.
func (s *Store) Search(_ context.Context, vector []float32, opts *vectorstore.SearchOptions) ([]vectorstore.SearchResult, error) {
	if err := tenancy.VectorSearch(opts); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	topK := 10
	if opts != nil && opts.TopK > 0 {
		topK = opts.TopK
	}

	var results []vectorstore.SearchResult
	for _, e := range s.entries {
		// Apply metadata filters.
		if opts != nil && len(opts.Filter) > 0 {
			if !matchesFilter(e.Metadata, opts.Filter) {
				continue
			}
		}

		// Apply tenant filter.
		if e.Metadata["tenant_id"] != opts.TenantKey {
			continue
		}

		score := cosineSimilarity(vector, e.Vector)

		if opts != nil && opts.MinScore > 0 && score < opts.MinScore {
			continue
		}

		results = append(results, vectorstore.SearchResult{
			Entry: e,
			Score: score,
		})
	}

	// Equal scores fall back to ID order, so two searches over the same
	// entries always agree. Map iteration order would otherwise decide.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})

	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

// Delete removes entries by their IDs.
func (s *Store) Delete(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range ids {
		delete(s.entries, id)
	}
	return nil
}

// DeleteByMetadata removes entries matching the given metadata filter. An
// empty filter would match every entry and is ErrInvalidArgument.
func (s *Store) DeleteByMetadata(_ context.Context, filter map[string]string) error {
	if err := tenancy.VectorDeleteFilter(filter); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, e := range s.entries {
		if matchesFilter(e.Metadata, filter) {
			delete(s.entries, id)
		}
	}
	return nil
}

// matchesFilter returns true if all filter key-value pairs exist in metadata.
func matchesFilter(metadata, filter map[string]string) bool {
	for k, v := range filter {
		if metadata[k] != v {
			return false
		}
	}
	return true
}

// cosineSimilarity computes the cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
