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

// CountCollections counts collections matching the filter.
func (e *Engine) CountCollections(ctx context.Context, filter *collection.CountFilter) (int64, error) {
	if e.store == nil {
		return 0, weave.ErrNoStore
	}
	return e.store.CountCollections(ctx, filter)
}

// CountDocuments counts documents matching the filter.
func (e *Engine) CountDocuments(ctx context.Context, filter *document.CountFilter) (int64, error) {
	if e.store == nil {
		return 0, weave.ErrNoStore
	}
	return e.store.CountDocuments(ctx, filter)
}

// CountChunks counts chunks matching the filter.
func (e *Engine) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	if e.store == nil {
		return 0, weave.ErrNoStore
	}
	return e.store.CountChunks(ctx, filter)
}

// GetChunk reads one chunk.
func (e *Engine) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	return e.store.GetChunk(ctx, chunkID)
}

// ListChunks pages chunks by document or by collection. A filter naming
// neither is refused: an unscoped chunk listing is a full table scan nobody
// asked for.
func (e *Engine) ListChunks(ctx context.Context, filter *chunk.ListFilter) ([]*chunk.Chunk, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	if filter == nil || (filter.DocumentID.String() == "" && filter.CollectionID.String() == "") {
		return nil, fmt.Errorf("%w: list chunks needs a document or a collection", weave.ErrInvalidArgument)
	}
	chunks, err := e.store.ListChunks(ctx, filter)
	if err != nil {
		return nil, err
	}
	if chunks == nil {
		// An empty page is [] on the wire, never null.
		chunks = []*chunk.Chunk{}
	}
	return chunks, nil
}
