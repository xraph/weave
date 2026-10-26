// Package memory provides an in-memory implementation of the Weave composite
// store. Suitable for testing and development.
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/tenancy"
	"github.com/xraph/weave/store"
)

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store is an in-memory implementation of the composite Weave store. Every
// data method scopes to the context tenant: another tenant's row is
// missing, exactly like an unknown ID.
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
func (s *Store) CreateCollection(ctx context.Context, col *collection.Collection) error {
	if _, err := tenancy.Owner(ctx, "collection", col.TenantID); err != nil {
		return err
	}
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
func (s *Store) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	col, ok := s.collections[colID.String()]
	if !ok || col.TenantID != tenant {
		return nil, weave.ErrCollectionNotFound
	}
	return col, nil
}

// GetCollectionByName retrieves a collection by tenant and name.
// tenantID must equal the context tenant.
func (s *Store) GetCollectionByName(ctx context.Context, tenantID, name string) (*collection.Collection, error) {
	tenant, err := tenancy.Scope(ctx, &tenantID)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, col := range s.collections {
		if col.TenantID == tenant && col.Name == name {
			return col, nil
		}
	}
	return nil, weave.ErrCollectionNotFound
}

// UpdateCollection persists changes to an existing collection.
func (s *Store) UpdateCollection(ctx context.Context, col *collection.Collection) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := col.ID.String()
	if existing, exists := s.collections[key]; !exists || existing.TenantID != tenant || col.TenantID != tenant {
		return weave.ErrCollectionNotFound
	}

	for k, existing := range s.collections {
		if k != key && existing.TenantID == col.TenantID && existing.Name == col.Name {
			return weave.ErrCollectionAlreadyExists
		}
	}

	col.UpdatedAt = time.Now().UTC()
	s.collections[key] = col
	return nil
}

// DeleteCollection removes a collection by ID with cascading deletes.
func (s *Store) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := colID.String()
	if existing, exists := s.collections[key]; !exists || existing.TenantID != tenant {
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
func (s *Store) ListCollections(ctx context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	tenant, err := tenancy.CollectionList(ctx, filter)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*collection.Collection, 0, len(s.collections))
	for _, col := range s.collections {
		if col.TenantID != tenant {
			continue
		}
		if filter != nil && filter.Search != "" && !strings.Contains(strings.ToLower(col.Name), strings.ToLower(filter.Search)) {
			continue
		}
		result = append(result, col)
	}

	desc := filter != nil && filter.SortDesc
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			if desc {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.CreatedAt.Before(b.CreatedAt)
		}
		if desc {
			return a.ID.String() > b.ID.String()
		}
		return a.ID.String() < b.ID.String()
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

// CountCollections returns the count of collections matching the filter.
func (s *Store) CountCollections(ctx context.Context, filter *collection.CountFilter) (int64, error) {
	tenant, err := tenancy.CollectionCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	for _, col := range s.collections {
		if col.TenantID != tenant {
			continue
		}
		if filter != nil && filter.Search != "" && !strings.Contains(strings.ToLower(col.Name), strings.ToLower(filter.Search)) {
			continue
		}
		count++
	}
	return count, nil
}

// ──────────────────────────────────────────────────
// Document operations
// ──────────────────────────────────────────────────

// CreateDocument persists a new document.
func (s *Store) CreateDocument(ctx context.Context, doc *document.Document) error {
	if _, err := tenancy.Owner(ctx, "document", doc.TenantID); err != nil {
		return err
	}
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
func (s *Store) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	doc, ok := s.documents[docID.String()]
	if !ok || doc.TenantID != tenant {
		return nil, weave.ErrDocumentNotFound
	}
	return doc, nil
}

// UpdateDocument persists changes to an existing document.
func (s *Store) UpdateDocument(ctx context.Context, doc *document.Document) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := doc.ID.String()
	if existing, exists := s.documents[key]; !exists || existing.TenantID != tenant || doc.TenantID != tenant {
		return weave.ErrDocumentNotFound
	}

	doc.UpdatedAt = time.Now().UTC()
	s.documents[key] = doc
	return nil
}

// DeleteDocument removes a document by ID with cascading chunk deletes.
func (s *Store) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := docID.String()
	if existing, exists := s.documents[key]; !exists || existing.TenantID != tenant {
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
func (s *Store) ListDocuments(ctx context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	tenant, err := tenancy.DocumentList(ctx, filter)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*document.Document, 0, len(s.documents))
	for _, doc := range s.documents {
		if doc.TenantID != tenant {
			continue
		}
		if filter != nil {
			if filter.CollectionID.String() != "" && doc.CollectionID.String() != filter.CollectionID.String() {
				continue
			}
			if filter.State != "" && doc.State != filter.State {
				continue
			}
			if filter.Search != "" && !strings.Contains(strings.ToLower(doc.Title), strings.ToLower(filter.Search)) {
				continue
			}
		}
		result = append(result, doc)
	}

	desc := filter != nil && filter.SortDesc
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			if desc {
				return a.CreatedAt.After(b.CreatedAt)
			}
			return a.CreatedAt.Before(b.CreatedAt)
		}
		if desc {
			return a.ID.String() > b.ID.String()
		}
		return a.ID.String() < b.ID.String()
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
func (s *Store) CountDocuments(ctx context.Context, filter *document.CountFilter) (int64, error) {
	tenant, err := tenancy.DocumentCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	for _, doc := range s.documents {
		if doc.TenantID != tenant {
			continue
		}
		if filter != nil {
			if filter.CollectionID.String() != "" && doc.CollectionID.String() != filter.CollectionID.String() {
				continue
			}
			if filter.State != "" && doc.State != filter.State {
				continue
			}
			if filter.Search != "" && !strings.Contains(strings.ToLower(doc.Title), strings.ToLower(filter.Search)) {
				continue
			}
			if !filter.UpdatedBefore.IsZero() && !doc.UpdatedAt.Before(filter.UpdatedBefore) {
				continue
			}
		}
		count++
	}
	return count, nil
}

// DeleteDocumentsByCollection removes all documents in a collection.
func (s *Store) DeleteDocumentsByCollection(ctx context.Context, colID id.CollectionID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := colID.String()
	for dk, doc := range s.documents {
		if doc.CollectionID.String() == key && doc.TenantID == tenant {
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

// CreateChunkBatch persists a batch of chunks. Every chunk must belong to
// the context tenant; one that doesn't refuses the whole batch.
func (s *Store) CreateChunkBatch(ctx context.Context, chunks []*chunk.Chunk) error {
	if _, err := weave.RequireTenant(ctx); err != nil {
		return err
	}
	for _, ch := range chunks {
		if _, err := tenancy.Owner(ctx, "chunk", ch.TenantID); err != nil {
			return err
		}
	}
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
func (s *Store) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	ch, ok := s.chunks[chunkID.String()]
	if !ok || ch.TenantID != tenant {
		return nil, weave.ErrChunkNotFound
	}
	return ch, nil
}

// ListChunksByDocument returns all chunks for a document, ordered by index.
func (s *Store) ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := docID.String()
	var result []*chunk.Chunk
	for _, ch := range s.chunks {
		if ch.DocumentID.String() == key && ch.TenantID == tenant {
			result = append(result, ch)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Index < result[j].Index
	})
	return result, nil
}

// DeleteChunksByDocument removes all chunks for a document.
func (s *Store) DeleteChunksByDocument(ctx context.Context, docID id.DocumentID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := docID.String()
	for ck, ch := range s.chunks {
		if ch.DocumentID.String() == key && ch.TenantID == tenant {
			delete(s.chunks, ck)
		}
	}
	return nil
}

// DeleteChunksByCollection removes all chunks for a collection.
func (s *Store) DeleteChunksByCollection(ctx context.Context, colID id.CollectionID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := colID.String()
	for ck, ch := range s.chunks {
		if ch.CollectionID.String() == key && ch.TenantID == tenant {
			delete(s.chunks, ck)
		}
	}
	return nil
}

// ListChunks returns chunks matching the filter, ordered by document then index.
func (s *Store) ListChunks(ctx context.Context, filter *chunk.ListFilter) ([]*chunk.Chunk, error) {
	tenant, err := tenancy.ChunkList(ctx, filter)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*chunk.Chunk
	for _, ch := range s.chunks {
		if ch.TenantID != tenant {
			continue
		}
		if filter != nil {
			if filter.DocumentID.String() != "" && ch.DocumentID.String() != filter.DocumentID.String() {
				continue
			}
			if filter.CollectionID.String() != "" && ch.CollectionID.String() != filter.CollectionID.String() {
				continue
			}
		}
		result = append(result, ch)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.DocumentID.String() != b.DocumentID.String() {
			return a.DocumentID.String() < b.DocumentID.String()
		}
		return a.Index < b.Index
	})
	if filter != nil {
		if filter.Offset >= len(result) {
			return nil, nil
		}
		result = result[filter.Offset:]
		if filter.Limit > 0 && filter.Limit < len(result) {
			result = result[:filter.Limit]
		}
	}
	return result, nil
}

// CountChunks returns the count of chunks matching the filter.
func (s *Store) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	tenant, err := tenancy.ChunkCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int64
	for _, ch := range s.chunks {
		if ch.TenantID != tenant {
			continue
		}
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
