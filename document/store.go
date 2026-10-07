package document

import (
	"context"
	"time"

	"github.com/xraph/weave/id"
)

// ListFilter controls pagination and filtering for document list queries.
type ListFilter struct {
	// CollectionID filters by collection. Empty means all collections.
	CollectionID id.CollectionID
	// State filters by document state. Empty means all states.
	State State
	// Search filters documents by title (case-insensitive substring match).
	Search string
	// Limit is the maximum number of documents to return. Zero means no limit.
	Limit int
	// Offset is the number of documents to skip.
	Offset int
	// SortDesc lists newest first. The default is oldest first, which API
	// clients paging by offset already depend on.
	SortDesc bool
	// Tenant filters by tenant. Nil means every tenant. A non-nil value is
	// an exact match, so a pointer to "" means rows written with no tenant.
	Tenant *string
}

// CountFilter controls filtering for document count queries.
type CountFilter struct {
	// CollectionID filters by collection. Empty means all collections.
	CollectionID id.CollectionID
	// State filters by document state. Empty means all states.
	State State
	// Search filters by title, matched exactly as ListFilter.Search.
	Search string
	// Tenant filters by tenant. Nil means every tenant. A non-nil value is
	// an exact match, so a pointer to "" means rows written with no tenant.
	Tenant *string
	// UpdatedBefore counts only documents last updated before this instant.
	// Zero means no filter.
	UpdatedBefore time.Time
}

// Store defines the persistence contract for documents.
type Store interface {
	// CreateDocument persists a new document.
	CreateDocument(ctx context.Context, doc *Document) error

	// GetDocument retrieves a document by ID.
	GetDocument(ctx context.Context, docID id.DocumentID) (*Document, error)

	// UpdateDocument persists changes to an existing document.
	UpdateDocument(ctx context.Context, doc *Document) error

	// DeleteDocument removes a document by ID.
	DeleteDocument(ctx context.Context, docID id.DocumentID) error

	// ListDocuments returns documents matching the given filter.
	ListDocuments(ctx context.Context, filter *ListFilter) ([]*Document, error)

	// CountDocuments returns the number of documents matching the given filter.
	CountDocuments(ctx context.Context, filter *CountFilter) (int64, error)

	// DeleteDocumentsByCollection removes all documents belonging to a collection.
	DeleteDocumentsByCollection(ctx context.Context, colID id.CollectionID) error
}
