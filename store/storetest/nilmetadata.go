package storetest

import (
	"context"
	"testing"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

// testNilMetadata writes a collection, a document and a chunk with no
// metadata map at all, which is what a caller that never sets one sends.
// Every write must succeed and read back by ID.
func testNilMetadata(t *testing.T, s store.Store) {
	ctx := context.Background()

	col := &collection.Collection{
		ID:            id.NewCollectionID(),
		Name:          "nil-metadata",
		ChunkStrategy: "recursive",
		ChunkSize:     64,
	}
	if err := s.CreateCollection(ctx, col); err != nil {
		t.Fatalf("create collection with nil metadata: %v", err)
	}
	if _, err := s.GetCollection(ctx, col.ID); err != nil {
		t.Errorf("get collection with nil metadata: %v", err)
	}

	doc := &document.Document{
		ID:           id.NewDocumentID(),
		CollectionID: col.ID,
		Title:        "no metadata",
		ContentHash:  "sha-nil-metadata",
		State:        document.StateReady,
	}
	if err := s.CreateDocument(ctx, doc); err != nil {
		t.Fatalf("create document with nil metadata: %v", err)
	}
	if _, err := s.GetDocument(ctx, doc.ID); err != nil {
		t.Errorf("get document with nil metadata: %v", err)
	}

	ch := &chunk.Chunk{
		ID:           id.NewChunkID(),
		DocumentID:   doc.ID,
		CollectionID: col.ID,
		Content:      "a chunk with no metadata",
	}
	if err := s.CreateChunkBatch(ctx, []*chunk.Chunk{ch}); err != nil {
		t.Fatalf("create chunk with nil metadata: %v", err)
	}
	got, err := s.GetChunk(ctx, ch.ID)
	if err != nil {
		t.Fatalf("get chunk with nil metadata: %v", err)
	}
	if got.Content != ch.Content {
		t.Errorf("chunk content: got %q, want %q", got.Content, ch.Content)
	}
}
