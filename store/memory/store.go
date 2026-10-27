// Package memory provides an in-memory implementation of the Weave composite
// store. Suitable for testing and development.
package memory

import (
	"context"
	"fmt"
	"slices"
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

// CreateDocument persists a new document. Its collection must belong to the
// context tenant, else ErrCollectionNotFound.
func (s *Store) CreateDocument(ctx context.Context, doc *document.Document) error {
	tenant, err := tenancy.Owner(ctx, "document", doc.TenantID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if col, ok := s.collections[doc.CollectionID.String()]; !ok || col.TenantID != tenant {
		return weave.ErrCollectionNotFound
	}

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
	existing, exists := s.documents[key]
	if !exists || existing.TenantID != tenant || doc.TenantID != tenant ||
		existing.CollectionID.String() != doc.CollectionID.String() {
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
			if filter.SourceKey != "" && doc.SourceKey != filter.SourceKey {
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

// TouchDocument records an ingest's heartbeat under the store mutex, so of
// several claims of one stalled document only the first finds it stalled.
func (s *Store) TouchDocument(ctx context.Context, docID id.DocumentID, staleBefore time.Time) error {
	return s.transition(ctx, docID, staleBefore, inProgress, func(d *document.Document) {
		d.State = document.StateProcessing
	})
}

// FailDocument marks a pending or processing document failed under the
// store mutex.
func (s *Store) FailDocument(ctx context.Context, docID id.DocumentID, errText string, staleBefore time.Time) error {
	return s.transition(ctx, docID, staleBefore, inProgress, func(d *document.Document) {
		d.State = document.StateFailed
		d.Error = errText
	})
}

// CompleteDocument moves a processing document to ready under the store
// mutex.
func (s *Store) CompleteDocument(ctx context.Context, docID id.DocumentID, chunkCount int) error {
	return s.transition(ctx, docID, time.Time{}, []document.State{document.StateProcessing}, func(d *document.Document) {
		d.State = document.StateReady
		d.ChunkCount = chunkCount
	})
}

// inProgress are the states TouchDocument and FailDocument move a document
// from.
var inProgress = []document.State{document.StatePending, document.StateProcessing}

// transition applies change to a copy of a document of the context tenant
// in one of the from states, last updated before staleBefore when that is
// set, and stores the copy with UpdatedAt now. The stored pointer is
// replaced, never edited, because callers hold the documents GetDocument
// returned.
func (s *Store) transition(ctx context.Context, docID id.DocumentID, staleBefore time.Time, from []document.State, change func(*document.Document)) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := docID.String()
	stored, ok := s.documents[key]
	if !ok || stored.TenantID != tenant {
		return weave.ErrDocumentNotFound
	}
	if !slices.Contains(from, stored.State) {
		return fmt.Errorf("%w: document is %s", weave.ErrInvalidState, stored.State)
	}
	if !staleBefore.IsZero() && !stored.UpdatedAt.Before(staleBefore) {
		return fmt.Errorf("%w: document was updated since it stalled", weave.ErrInvalidState)
	}
	next := *stored
	change(&next)
	next.UpdatedAt = time.Now().UTC()
	s.documents[key] = &next
	return nil
}

// ListStalledDocuments lists, across tenants, the pending, processing and
// superseded documents last updated before before, oldest first.
func (s *Store) ListStalledDocuments(_ context.Context, before time.Time, limit int) ([]*document.Document, error) {
	if limit <= 0 {
		limit = store.DefaultStalledLimit
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*document.Document
	for _, doc := range s.documents {
		if doc.TenantID != "" && isStalled(doc, before) {
			result = append(result, doc)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			return a.UpdatedAt.Before(b.UpdatedAt)
		}
		return a.ID.String() < b.ID.String()
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

// CountStalledUntenanted counts the stalled rows with an empty tenant.
func (s *Store) CountStalledUntenanted(_ context.Context, before time.Time) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int64
	for _, doc := range s.documents {
		if doc.TenantID == "" && isStalled(doc, before) {
			n++
		}
	}
	return n, nil
}

// isStalled reports whether doc is pending, processing or superseded and
// last updated before before.
func isStalled(doc *document.Document, before time.Time) bool {
	switch doc.State {
	case document.StatePending, document.StateProcessing, document.StateSuperseded:
		return doc.UpdatedAt.Before(before)
	}
	return false
}

// ReplaceSourceDocument supersedes the source's ready and failed documents,
// swaps their chunks for chunks and makes doc ready, all under the store
// mutex. Everything is checked before the first change, so a refused call
// leaves the store as it was.
func (s *Store) ReplaceSourceDocument(ctx context.Context, doc *document.Document, chunks []*chunk.Chunk) ([]id.DocumentID, error) {
	tenant, err := tenancy.SourceReplace(ctx, doc, chunks)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := doc.ID.String()
	stored, ok := s.documents[key]
	if !ok || stored.TenantID != tenant || stored.CollectionID.String() != doc.CollectionID.String() {
		return nil, weave.ErrDocumentNotFound
	}
	if stored.SourceKey != doc.SourceKey {
		return nil, fmt.Errorf("%w: source key does not match the stored document's", weave.ErrInvalidArgument)
	}
	if stored.State != document.StateProcessing {
		return nil, fmt.Errorf("%w: document is %s, not processing", weave.ErrInvalidState, stored.State)
	}
	seen := make(map[string]bool, len(chunks))
	for _, ch := range chunks {
		chunkKey := ch.ID.String()
		if _, exists := s.chunks[chunkKey]; exists || seen[chunkKey] {
			return nil, fmt.Errorf("weave: replace source document: chunk %s already exists", chunkKey)
		}
		seen[chunkKey] = true
	}

	now := time.Now().UTC()
	var replaced []id.DocumentID
	for dk, d := range s.documents {
		if d.TenantID != tenant || d.CollectionID.String() != stored.CollectionID.String() ||
			d.SourceKey != stored.SourceKey ||
			(d.State != document.StateReady && d.State != document.StateFailed) {
			continue
		}
		superseded := *d
		superseded.State = document.StateSuperseded
		superseded.UpdatedAt = now
		s.documents[dk] = &superseded
		replaced = append(replaced, d.ID)
		for ck, ch := range s.chunks {
			if ch.DocumentID.String() == dk {
				delete(s.chunks, ck)
			}
		}
	}
	sort.Slice(replaced, func(i, j int) bool { return replaced[i].String() < replaced[j].String() })

	for _, ch := range chunks {
		ch.CreatedAt = now
		s.chunks[ch.ID.String()] = ch
	}
	ready := *stored
	ready.State = document.StateReady
	ready.ChunkCount = len(chunks)
	ready.UpdatedAt = now
	s.documents[key] = &ready
	doc.State, doc.ChunkCount, doc.UpdatedAt = ready.State, ready.ChunkCount, now
	return replaced, nil
}

// ──────────────────────────────────────────────────
// Chunk operations
// ──────────────────────────────────────────────────

// CreateChunkBatch persists a batch of chunks. Every chunk must belong to
// the context tenant and to a document of that tenant, with the document's
// collection, and its ID must be new. Any chunk that fails refuses the whole
// batch before anything is written, as the SQL backends' single INSERT does.
func (s *Store) CreateChunkBatch(ctx context.Context, chunks []*chunk.Chunk) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	for _, ch := range chunks {
		if _, err := tenancy.Owner(ctx, "chunk", ch.TenantID); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]bool, len(chunks))
	for _, ch := range chunks {
		key := ch.ID.String()
		if _, exists := s.chunks[key]; exists || seen[key] {
			return fmt.Errorf("weave: create chunk batch: chunk %s already exists", key)
		}
		seen[key] = true
		doc, ok := s.documents[ch.DocumentID.String()]
		if !ok || doc.TenantID != tenant {
			return weave.ErrDocumentNotFound
		}
		if doc.CollectionID.String() != ch.CollectionID.String() {
			return fmt.Errorf("%w: chunk collection does not match its document's", weave.ErrInvalidArgument)
		}
	}

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
