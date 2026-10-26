// Package store defines the composite metadata store interface for Weave.
// It aggregates collection, document, and chunk store operations with
// lifecycle management (migrations, health checks, shutdown).
package store

import (
	"context"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
)

// Store is the composite metadata store interface for Weave.
// It embeds the subsystem store interfaces for collections, documents,
// and chunks, plus lifecycle management methods.
//
// Every method except Migrate, Ping and Close works within the context
// tenant (weave.WithTenant) and returns weave.ErrTenantRequired, before any
// I/O, when there is none. A get, update or delete by ID of another tenant's
// row returns the same not-found sentinel as an unknown ID, bulk deletes and
// every list and count see only the context tenant's rows, and a create
// whose TenantID differs from the context tenant is weave.ErrInvalidArgument.
// store/storetest checks all of it.
type Store interface {
	document.Store
	collection.Store
	chunk.Store

	// Migrate runs any pending database migrations.
	Migrate(ctx context.Context) error

	// Ping verifies the store connection is alive.
	Ping(ctx context.Context) error

	// Close releases all store resources.
	Close() error
}
