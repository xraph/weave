package engine_test

import (
	"context"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/engine"
)

// tagChunker is a chunker that puts its own metadata on every chunk.
type tagChunker struct {
	inner chunker.Chunker
	meta  map[string]string
}

func (c tagChunker) Chunk(ctx context.Context, text string, opts *chunker.Options) ([]chunker.ChunkResult, error) {
	out, err := c.inner.Chunk(ctx, text, opts)
	for i := range out {
		out[i].Metadata = map[string]string{}
		for k, v := range c.meta {
			out[i].Metadata[k] = v
		}
	}
	return out, err
}

func TestDocumentMetadataReachesItsChunks(t *testing.T) {
	r := newRig(t)
	ctx := weave.WithTenant(context.Background(), "t1")
	col := mustTestCollection(t, r.Engine, "doc-metadata")
	res, err := r.Engine.Ingest(ctx, &engine.IngestInput{
		CollectionID: col.ID,
		Title:        "refunds",
		Content:      "refunds are issued within thirty days",
		Metadata:     map[string]string{"kind": "report", "project": "p1"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	hits, err := r.Engine.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID))
	if err != nil || len(hits) == 0 {
		t.Fatalf("retrieve: %v, %d hits", err, len(hits))
	}
	md := hits[0].Chunk.Metadata
	if md["kind"] != "report" || md["project"] != "p1" {
		t.Errorf("search hit metadata %v, want the document's kind and project", md)
	}
	if md["document_id"] != res.DocumentID.String() {
		t.Errorf("document_id %q, want %q", md["document_id"], res.DocumentID)
	}

	chunks, err := r.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: res.DocumentID})
	if err != nil || len(chunks) == 0 {
		t.Fatalf("list chunks: %v, %d", err, len(chunks))
	}
	if chunks[0].Metadata["kind"] != "report" {
		t.Errorf("stored chunk metadata %v, want the document's kind", chunks[0].Metadata)
	}
}

func TestChunkMetadataWinsOverDocumentMetadata(t *testing.T) {
	r := newRig(t, withOpt(engine.WithChunker(tagChunker{
		inner: chunker.NewRecursiveChunker(),
		meta:  map[string]string{"kind": "chunk-kind", "section": "intro"},
	})))
	ctx := weave.WithTenant(context.Background(), "t1")
	col := mustTestCollection(t, r.Engine, "chunk-wins")
	res, err := r.Engine.Ingest(ctx, &engine.IngestInput{
		CollectionID: col.ID,
		Title:        "refunds",
		Content:      "refunds are issued within thirty days",
		Metadata:     map[string]string{"kind": "doc-kind", "project": "p1"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	chunks, err := r.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: res.DocumentID})
	if err != nil || len(chunks) == 0 {
		t.Fatalf("list chunks: %v, %d", err, len(chunks))
	}
	md := chunks[0].Metadata
	if md["kind"] != "chunk-kind" || md["section"] != "intro" || md["project"] != "p1" {
		t.Errorf("chunk metadata %v, want chunk kind and section kept and the document's project added", md)
	}
	hits, err := r.Engine.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID))
	if err != nil || len(hits) == 0 {
		t.Fatalf("retrieve: %v, %d hits", err, len(hits))
	}
	if got := hits[0].Chunk.Metadata["kind"]; got != "chunk-kind" {
		t.Errorf("search hit kind %q, want chunk-kind", got)
	}
}

func TestDocumentMetadataCannotOverwriteWeaveKeys(t *testing.T) {
	r := newRig(t)
	ctx := weave.WithTenant(context.Background(), "t1")
	col := mustTestCollection(t, r.Engine, "reserved")
	res, err := r.Engine.Ingest(ctx, &engine.IngestInput{
		CollectionID: col.ID,
		Title:        "x",
		Content:      "refunds are issued within thirty days",
		Metadata:     map[string]string{"tenant_id": "t2", "document_id": "doc_fake"},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	hits, err := r.Engine.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID))
	if err != nil || len(hits) == 0 {
		t.Fatalf("retrieve: %v, %d hits", err, len(hits))
	}
	md := hits[0].Chunk.Metadata
	if md["tenant_id"] != "t1" || md["document_id"] != res.DocumentID.String() {
		t.Errorf("metadata %v, want weave's own tenant_id and document_id", md)
	}
}
