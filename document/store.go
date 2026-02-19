package document

import (
	"context"

	"github.com/xraph/weave/id"
)

// ListFilter controls pagination and filtering for document list queries.
type ListFilter struct {
	// CollectionID filters by collection. Empty means all collections.
	CollectionID id.CollectionID
	// State filters by document state. Empty means all states.
	State State
	// Limit is the maximum number of documents to return. Zero means no limit.
	Limit int
	// Offset is the number of documents to skip.
	Offset int
}

// CountFilter controls filtering for document count queries.
type CountFilter struct {
	// CollectionID filters by collection. Empty means all collections.
	CollectionID id.CollectionID
	// State filters by document state. Empty means all states.
	State State
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
