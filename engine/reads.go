package engine

import (
	"context"
	"fmt"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
)

// CountCollections counts the context tenant's collections matching the
// filter.
func (e *Engine) CountCollections(ctx context.Context, filter *collection.CountFilter) (int64, error) {
	if e.store == nil {
		return 0, weave.ErrNoStore
	}
	ctx, _, err := e.tenant(ctx)
	if err != nil {
		return 0, err
	}
	return e.store.CountCollections(ctx, filter)
}

// CountDocuments counts the context tenant's documents matching the filter.
func (e *Engine) CountDocuments(ctx context.Context, filter *document.CountFilter) (int64, error) {
	if e.store == nil {
		return 0, weave.ErrNoStore
	}
	ctx, _, err := e.tenant(ctx)
	if err != nil {
		return 0, err
	}
	return e.store.CountDocuments(ctx, filter)
}

// CountChunks counts the context tenant's chunks matching the filter.
func (e *Engine) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	if e.store == nil {
		return 0, weave.ErrNoStore
	}
	ctx, _, err := e.tenant(ctx)
	if err != nil {
		return 0, err
	}
	return e.store.CountChunks(ctx, filter)
}

// GetChunk reads one chunk within the context tenant.
func (e *Engine) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := e.store.GetChunk(ctx, chunkID)
	if err != nil {
		return nil, err
	}
	return ownChunk(ch, tenant)
}

// ListChunks pages the context tenant's chunks by document or by
// collection. A filter naming
// neither is refused: an unscoped chunk listing is a full table scan nobody
// asked for.
func (e *Engine) ListChunks(ctx context.Context, filter *chunk.ListFilter) ([]*chunk.Chunk, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return nil, err
	}
	if filter == nil || (filter.DocumentID.String() == "" && filter.CollectionID.String() == "") {
		return nil, fmt.Errorf("%w: list chunks needs a document or a collection", weave.ErrInvalidArgument)
	}
	chunks, err := e.store.ListChunks(ctx, filter)
	if err != nil {
		return nil, err
	}
	chunks = owned(chunks, tenant, chunkTenant)
	if chunks == nil {
		// An empty page is [] on the wire, never null.
		chunks = []*chunk.Chunk{}
	}
	return chunks, nil
}
