package collection

import (
	"context"

	"github.com/xraph/weave/id"
)

// ListFilter controls pagination and filtering for collection list queries.
type ListFilter struct {
	// Search filters collections by name (case-insensitive substring match).
	Search string
	// Limit is the maximum number of collections to return. Zero means no limit.
	Limit int
	// Offset is the number of collections to skip.
	Offset int
	// SortDesc lists newest first. The default is oldest first, which API
	// clients paging by offset already depend on.
	SortDesc bool
	// Tenant is the old explicit tenant filter.
	//
	// Deprecated: the tenant comes from the context (weave.WithTenant). A non-nil value must equal it.
	Tenant *string
}

// CountFilter controls filtering for collection count queries.
type CountFilter struct {
	// Search filters collections by name (case-insensitive substring match).
	Search string
	// Tenant is the old explicit tenant filter.
	//
	// Deprecated: the tenant comes from the context (weave.WithTenant). A non-nil value must equal it.
	Tenant *string
}

// Store defines the persistence contract for collections. Every method is
// scoped to the context tenant; see store.Store for the rules.
type Store interface {
	// CreateCollection persists a new collection. Its TenantID must be the
	// context tenant.
	CreateCollection(ctx context.Context, col *Collection) error

	// GetCollection retrieves a collection by ID.
	GetCollection(ctx context.Context, colID id.CollectionID) (*Collection, error)

	// GetCollectionByName retrieves a collection by tenant and name.
	// tenantID must equal the context tenant, else weave.ErrInvalidArgument.
	GetCollectionByName(ctx context.Context, tenantID, name string) (*Collection, error)

	// UpdateCollection persists changes to an existing collection.
	UpdateCollection(ctx context.Context, col *Collection) error

	// DeleteCollection removes a collection by ID.
	DeleteCollection(ctx context.Context, colID id.CollectionID) error

	// ListCollections returns collections matching the given filter.
	ListCollections(ctx context.Context, filter *ListFilter) ([]*Collection, error)

	// CountCollections returns the number of collections matching the given filter.
	CountCollections(ctx context.Context, filter *CountFilter) (int64, error)
}
