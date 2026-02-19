package collection

import (
	"context"

	"github.com/xraph/weave/id"
)

// ListFilter controls pagination and filtering for collection list queries.
type ListFilter struct {
	// Limit is the maximum number of collections to return. Zero means no limit.
	Limit int
	// Offset is the number of collections to skip.
	Offset int
}

// Store defines the persistence contract for collections.
type Store interface {
	// CreateCollection persists a new collection.
	CreateCollection(ctx context.Context, col *Collection) error

	// GetCollection retrieves a collection by ID.
	GetCollection(ctx context.Context, colID id.CollectionID) (*Collection, error)

	// GetCollectionByName retrieves a collection by tenant and name.
	GetCollectionByName(ctx context.Context, tenantID, name string) (*Collection, error)

	// UpdateCollection persists changes to an existing collection.
	UpdateCollection(ctx context.Context, col *Collection) error

	// DeleteCollection removes a collection by ID.
	DeleteCollection(ctx context.Context, colID id.CollectionID) error

	// ListCollections returns collections matching the given filter.
	ListCollections(ctx context.Context, filter *ListFilter) ([]*Collection, error)
}
