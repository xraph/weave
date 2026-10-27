// Package store defines the composite metadata store interface for Weave.
// It aggregates collection, document, and chunk store operations with
// lifecycle management (migrations, health checks, shutdown).
package store

import (
	"context"
	"time"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

// Store is the composite metadata store interface for Weave.
// It embeds the subsystem store interfaces for collections, documents,
// and chunks, plus lifecycle management methods.
//
// Every method except Migrate, Ping, Close, ListStalledDocuments and
// CountStalledUntenanted works within the context tenant (weave.WithTenant)
// and returns weave.ErrTenantRequired, before any I/O, when there is none.
// ListStalledDocuments is the one read across tenants, for recovery, which
// then works on each document under that document's own tenant;
// CountStalledUntenanted only counts rows that belong to no tenant. A get, update or delete by ID of another tenant's
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

	// TouchDocument records a heartbeat for an ingest in progress: it moves
	// a pending or processing document to processing and sets its UpdatedAt
	// to now. With a non-zero staleBefore it does so only when the document
	// was last updated before staleBefore, which makes it a claim: of several
	// callers that saw the same stalled document, exactly one succeeds. A
	// document in any other state, or updated since staleBefore, is
	// weave.ErrInvalidState and nothing is written; a missing document or
	// another tenant's is weave.ErrDocumentNotFound.
	TouchDocument(ctx context.Context, docID id.DocumentID, staleBefore time.Time) error

	// FailDocument moves a pending or processing document to failed, with
	// errText as its Error, and sets its UpdatedAt to now, in one guarded
	// write: a document another attempt has just made ready is never
	// overwritten. With a non-zero staleBefore it does so only when the
	// document was last updated before staleBefore, so a document whose
	// ingest sent a heartbeat since is left alone. The refusals are
	// TouchDocument's: weave.ErrInvalidState or weave.ErrDocumentNotFound,
	// and nothing written.
	FailDocument(ctx context.Context, docID id.DocumentID, errText string, staleBefore time.Time) error

	// CompleteDocument finishes an unkeyed ingest: it moves a processing
	// document to ready, with ChunkCount set to chunkCount and UpdatedAt to
	// now, in one guarded write. A document in any other state (recovery
	// failed it, say) is weave.ErrInvalidState and nothing is written, so a
	// failed document never comes back as ready; a missing document or
	// another tenant's is weave.ErrDocumentNotFound.
	CompleteDocument(ctx context.Context, docID id.DocumentID, chunkCount int) error

	// ListStalledDocuments is the one read that spans tenants, for recovery:
	// documents in pending or processing, or superseded, last updated before
	// before, oldest first. limit <= 0 means 100. It needs no context tenant.
	// Rows with an empty tenant (written before v1.9) are left out, so they
	// can never fill a batch; CountStalledUntenanted counts them.
	ListStalledDocuments(ctx context.Context, before time.Time, limit int) ([]*document.Document, error)

	// CountStalledUntenanted counts the rows ListStalledDocuments leaves out
	// for having an empty tenant: pending, processing or superseded, last
	// updated before before. Recovery reports the number and touches none of
	// them. It needs no context tenant and reads no tenant's rows.
	CountStalledUntenanted(ctx context.Context, before time.Time) (int64, error)

	// Migrate runs any pending database migrations.
	Migrate(ctx context.Context) error

	// Ping verifies the store connection is alive.
	Ping(ctx context.Context) error

	// Close releases all store resources.
	Close() error
}

// DefaultStalledLimit is how many documents ListStalledDocuments returns
// when it is given a limit of zero or less.
const DefaultStalledLimit = 100
