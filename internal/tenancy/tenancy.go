// Package tenancy holds the tenant checks every Weave metadata store runs
// before it touches data.
package tenancy

import (
	"context"
	"fmt"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
)

// Scope returns the context tenant. A non-nil filter must equal it; anything
// else, "" included, is ErrInvalidArgument.
func Scope(ctx context.Context, filter *string) (string, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return "", err
	}
	if filter != nil && *filter != tenant {
		return "", fmt.Errorf("%w: tenant filter does not match the context tenant", weave.ErrInvalidArgument)
	}
	return tenant, nil
}

// Owner returns the context tenant when owner, the tenant on an entity being
// created, equals it. Any other owner, "" included, is ErrInvalidArgument
// naming what (collection, document or chunk).
func Owner(ctx context.Context, what, owner string) (string, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return "", err
	}
	if owner != tenant {
		return "", fmt.Errorf("%w: tenant on the %s does not match the context tenant", weave.ErrInvalidArgument, what)
	}
	return tenant, nil
}

// The filter helpers below are Scope for each list and count filter, any of
// which may be nil. They are the only readers of the deprecated Tenant
// fields.

// CollectionList scopes a collection list filter.
func CollectionList(ctx context.Context, f *collection.ListFilter) (string, error) {
	if f == nil {
		return Scope(ctx, nil)
	}
	return Scope(ctx, f.Tenant) //nolint:staticcheck // checked against the context, never used to widen it
}

// CollectionCount scopes a collection count filter.
func CollectionCount(ctx context.Context, f *collection.CountFilter) (string, error) {
	if f == nil {
		return Scope(ctx, nil)
	}
	return Scope(ctx, f.Tenant) //nolint:staticcheck // checked against the context, never used to widen it
}

// DocumentList scopes a document list filter.
func DocumentList(ctx context.Context, f *document.ListFilter) (string, error) {
	if f == nil {
		return Scope(ctx, nil)
	}
	return Scope(ctx, f.Tenant) //nolint:staticcheck // checked against the context, never used to widen it
}

// DocumentCount scopes a document count filter.
func DocumentCount(ctx context.Context, f *document.CountFilter) (string, error) {
	if f == nil {
		return Scope(ctx, nil)
	}
	return Scope(ctx, f.Tenant) //nolint:staticcheck // checked against the context, never used to widen it
}

// ChunkList scopes a chunk list filter.
func ChunkList(ctx context.Context, f *chunk.ListFilter) (string, error) {
	if f == nil {
		return Scope(ctx, nil)
	}
	return Scope(ctx, f.Tenant) //nolint:staticcheck // checked against the context, never used to widen it
}

// ChunkCount scopes a chunk count filter.
func ChunkCount(ctx context.Context, f *chunk.CountFilter) (string, error) {
	if f == nil {
		return Scope(ctx, nil)
	}
	return Scope(ctx, f.Tenant) //nolint:staticcheck // checked against the context, never used to widen it
}
