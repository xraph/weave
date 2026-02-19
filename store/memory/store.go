// Package memory provides an in-memory implementation of the Weave composite
// store. Suitable for testing and development.
package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store is an in-memory implementation of the composite Weave store.
type Store struct {
	mu          sync.RWMutex
	collections map[string]*collection.Collection
	documents   map[string]*document.Document
	chunks      map[string]*chunk.Chunk
}

// New creates a new in-memory store.
func New() *Store {
	return &Store{
		collections: make(map[string]*collection.Collection),
		documents:   make(map[string]*document.Document),
		chunks:      make(map[string]*chunk.Chunk),
	}
}

// ──────────────────────────────────────────────────
// Lifecycle
// ──────────────────────────────────────────────────

// Migrate is a no-op for the in-memory store.
func (s *Store) Migrate(_ context.Context) error { return nil }

// Ping is a no-op for the in-memory store.
func (s *Store) Ping(_ context.Context) error { return nil }

// Close is a no-op for the in-memory store.
func (s *Store) Close() error { return nil }

// ──────────────────────────────────────────────────
// Collection operations
// ──────────────────────────────────────────────────

// CreateCollection persists a new collection.
func (s *Store) CreateCollection(_ context.Context, col *collection.Collection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := col.ID.String()
	if _, exists := s.collections[key]; exists {
		return weave.ErrCollectionAlreadyExists
	}

	for _, existing := range s.collections {
		if existing.TenantID == col.TenantID && existing.Name == col.Name {
			return weave.ErrCollectionAlreadyExists
		}
	}

	now := time.Now().UTC()
	col.CreatedAt = now
	col.UpdatedAt = now
	s.collections[key] = col
	return nil
}

// GetCollection retrieves a collection by ID.
func (s *Store) GetCollection(_ context.Context, colID id.CollectionID) (*collection.Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	col, ok := s.collections[colID.String()]
	if !ok {
		return nil, weave.ErrCollectionNotFound
	}
	return col, nil
}

// GetCollectionByName retrieves a collection by tenant and name.
func (s *Store) GetCollectionByName(_ context.Context, tenantID, name string) (*collection.Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, col := range s.collections {
		if col.TenantID == tenantID && col.Name == name {
			return col, nil
		}
	}
	return nil, weave.ErrCollectionNotFound
}

// UpdateCollection persists changes to an existing collection.
func (s *Store) UpdateCollection(_ context.Context, col *collection.Collection) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := col.ID.String()
	if _, exists := s.collections[key]; !exists {
		return weave.ErrCollectionNotFound
	}

	col.UpdatedAt = time.Now().UTC()
	s.collections[key] = col
	return nil
}

// DeleteCollection removes a collection by ID with cascading deletes.
func (s *Store) DeleteCollection(_ context.Context, colID id.CollectionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := colID.String()
	if _, exists := s.collections[key]; !exists {
		return weave.ErrCollectionNotFound
	}

	delete(s.collections, key)

	for dk, doc := range s.documents {
		if doc.CollectionID.String() == key {
			delete(s.documents, dk)
		}
	}
	for ck, ch := range s.chunks {
		if ch.CollectionID.String() == key {
			delete(s.chunks, ck)
		}
	}
	return nil
}

// ListCollections returns collections matching the given filter.
func (s *Store) ListCollections(_ context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*collection.Collection, 0, len(s.collections))
	for _, col := range s.collections {
		result = append(result, col)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	if filter != nil {
		if filter.Offset > 0 && filter.Offset < len(result) {
			result = result[filter.Offset:]
		} else if filter.Offset >= len(result) {
			return nil, nil
		}
		if filter.Limit > 0 && filter.Limit < len(result) {
			result = result[:filter.Limit]
		}
	}
	return result, nil
}

// ──────────────────────────────────────────────────
// Document operations
// ──────────────────────────────────────────────────

// CreateDocument persists a new document.
func (s *Store) CreateDocument(_ context.Context, doc *document.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := doc.ID.String()
	if _, exists := s.documents[key]; exists {
		return weave.ErrDocumentAlreadyExists
	}

	for _, existing := range s.documents {
		if existing.CollectionID.String() == doc.CollectionID.String() &&
			existing.ContentHash == doc.ContentHash {
			return weave.ErrDuplicateDocument
		}
	}

	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	s.documents[key] = doc
	return nil
}

// GetDocument retrieves a document by ID.
func (s *Store) GetDocument(_ context.Context, docID id.DocumentID) (*document.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	doc, ok := s.documents[docID.String()]
	if !ok {
		return nil, weave.ErrDocumentNotFound
	}
	return doc, nil
}

// UpdateDocument persists changes to an existing document.
func (s *Store) UpdateDocument(_ context.Context, doc *document.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := doc.ID.String()
	if _, exists := s.documents[key]; !exists {
		return weave.ErrDocumentNotFound
	}

	doc.UpdatedAt = time.Now().UTC()
	s.documents[key] = doc
	return nil
}

// DeleteDocument removes a document by ID with cascading chunk deletes.
func (s *Store) DeleteDocument(_ context.Context, docID id.DocumentID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := docID.String()
	if _, exists := s.documents[key]; !exists {
		return weave.ErrDocumentNotFound
	}

	delete(s.documents, key)

	for ck, ch := range s.chunks {
		if ch.DocumentID.String() == key {
			delete(s.chunks, ck)
		}
	}
	return nil
}

// ListDocuments returns documents matching the given filter.
func (s *Store) ListDocuments(_ context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*document.Document, 0, len(s.documents))
	for _, doc := range s.documents {
		if filter != nil {
			if filter.CollectionID.String() != "" && doc.CollectionID.String() != filter.CollectionID.String() {
				continue
			}
			if filter.State != "" && doc.State != filter.State {
				continue
			}
		}
		result = append(result, doc)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	if filter != nil {
		if filter.Offset > 0 && filter.Offset < len(result) {
			result = result[filter.Offset:]
		} else if filter.Offset >= len(result) {
			return nil, nil
		}
		if filter.Limit > 0 && filter.Limit < len(result) {
			result = result[:filter.Limit]
		}
	}
	return result, nil
}

// CountDocuments returns the count of documents matching the filter.
func (s *Store) CountDocuments(_ context.Context, filter *document.CountFilter) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	for _, doc := range s.documents {
		if filter != nil {
			if filter.CollectionID.String() != "" && doc.CollectionID.String() != filter.CollectionID.String() {
				continue
			}
			if filter.State != "" && doc.State != filter.State {
				continue
			}
		}
		count++
	}
	return count, nil
}

// DeleteDocumentsByCollection removes all documents in a collection.
func (s *Store) DeleteDocumentsByCollection(_ context.Context, colID id.CollectionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := colID.String()
	for dk, doc := range s.documents {
		if doc.CollectionID.String() == key {
			for ck, ch := range s.chunks {
				if ch.DocumentID.String() == dk {
					delete(s.chunks, ck)
				}
			}
			delete(s.documents, dk)
		}
	}
	return nil
}

// ──────────────────────────────────────────────────
// Chunk operations
// ──────────────────────────────────────────────────

// CreateChunkBatch persists a batch of chunks.
func (s *Store) CreateChunkBatch(_ context.Context, chunks []*chunk.Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	for _, ch := range chunks {
		ch.CreatedAt = now
		s.chunks[ch.ID.String()] = ch
	}
	return nil
}

// GetChunk retrieves a chunk by ID.
func (s *Store) GetChunk(_ context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ch, ok := s.chunks[chunkID.String()]
	if !ok {
		return nil, weave.ErrChunkNotFound
	}
	return ch, nil
}

// ListChunksByDocument returns all chunks for a document, ordered by index.
func (s *Store) ListChunksByDocument(_ context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := docID.String()
	var result []*chunk.Chunk
	for _, ch := range s.chunks {
		if ch.DocumentID.String() == key {
			result = append(result, ch)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Index < result[j].Index
	})
	return result, nil
}

// DeleteChunksByDocument removes all chunks for a document.
func (s *Store) DeleteChunksByDocument(_ context.Context, docID id.DocumentID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := docID.String()
	for ck, ch := range s.chunks {
		if ch.DocumentID.String() == key {
			delete(s.chunks, ck)
		}
	}
	return nil
}

// DeleteChunksByCollection removes all chunks for a collection.
func (s *Store) DeleteChunksByCollection(_ context.Context, colID id.CollectionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := colID.String()
	for ck, ch := range s.chunks {
		if ch.CollectionID.String() == key {
			delete(s.chunks, ck)
		}
	}
	return nil
}

// CountChunks returns the count of chunks matching the filter.
func (s *Store) CountChunks(_ context.Context, filter *chunk.CountFilter) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	for _, ch := range s.chunks {
		if filter != nil {
			if filter.CollectionID.String() != "" && ch.CollectionID.String() != filter.CollectionID.String() {
				continue
			}
			if filter.DocumentID.String() != "" && ch.DocumentID.String() != filter.DocumentID.String() {
				continue
			}
		}
		count++
	}
	return count, nil
}
