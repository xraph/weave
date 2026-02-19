package chunk

import (
	"context"

	"github.com/xraph/weave/id"
)

// CountFilter controls filtering for chunk count queries.
type CountFilter struct {
	// CollectionID filters by collection. Empty means all collections.
	CollectionID id.CollectionID
	// DocumentID filters by document. Empty means all documents.
	DocumentID id.DocumentID
}

// Store defines the persistence contract for chunks.
type Store interface {
	// CreateChunkBatch persists a batch of chunks.
	CreateChunkBatch(ctx context.Context, chunks []*Chunk) error

	// GetChunk retrieves a chunk by ID.
	GetChunk(ctx context.Context, chunkID id.ChunkID) (*Chunk, error)

	// ListChunksByDocument returns all chunks belonging to a document, ordered by index.
	ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*Chunk, error)

	// DeleteChunksByDocument removes all chunks belonging to a document.
	DeleteChunksByDocument(ctx context.Context, docID id.DocumentID) error

	// DeleteChunksByCollection removes all chunks belonging to a collection.
	DeleteChunksByCollection(ctx context.Context, colID id.CollectionID) error

	// CountChunks returns the number of chunks matching the given filter.
	CountChunks(ctx context.Context, filter *CountFilter) (int64, error)
}
