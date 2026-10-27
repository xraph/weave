// Package store defines the composite metadata store interface for Weave.
// It aggregates collection, document, and chunk store operations with
// lifecycle management (migrations, health checks, shutdown).
package store

import (
	"context"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
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
// A document's collection and a chunk's document must belong to the context
// tenant (else the parent's not-found sentinel), a chunk sits in its
// document's collection, a document never changes collection, and a chunk ID
// is never reused. store/storetest checks all of it.
type Store interface {
	document.Store
	collection.Store
	chunk.Store

	// ReplaceSourceDocument finishes a source-keyed ingest atomically: it marks
	// every ready or failed document with doc's collection, tenant and SourceKey
	// superseded and deletes their chunks, inserts chunks, and moves doc from
	// processing to ready with ChunkCount set. It returns the superseded IDs.
	// doc must be processing (ErrInvalidState otherwise, nothing written) and
	// have a SourceKey (ErrInvalidArgument).
	//
	// The stored document is read under the context tenant before anything is
	// written: missing, another tenant's or in another collection is
	// weave.ErrDocumentNotFound, and a SourceKey that differs from the stored
	// one is weave.ErrInvalidArgument. Every chunk must belong to doc, its
	// collection and the context tenant (weave.ErrInvalidArgument) and carry a
	// new ID. Concurrent replaces of one source serialize, so the source ends
	// with one ready document, the last to finish.
	ReplaceSourceDocument(ctx context.Context, doc *document.Document, chunks []*chunk.Chunk) ([]id.DocumentID, error)

	// Migrate runs any pending database migrations.
	Migrate(ctx context.Context) error

	// Ping verifies the store connection is alive.
	Ping(ctx context.Context) error

	// Close releases all store resources.
	Close() error
}
