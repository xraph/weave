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
