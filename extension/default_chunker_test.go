package extension

import (
	"context"
	"testing"

	"github.com/xraph/forge"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/store/memory"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

func registerForIngest(t *testing.T, opts ...ExtOption) *engine.Engine {
	t.Helper()
	base := make([]ExtOption, 0, 4+len(opts))
	base = append(base, WithStore(memory.New()), WithVectorStore(vsmemory.New()), WithDisableRoutes(),
		WithEngineOption(engine.WithEmbedder(embedtest.New(64))))
	ext := New(append(base, opts...)...)
	if err := ext.Register(forge.New(forge.WithAppName("t"))); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return ext.Engine()
}

func TestExtensionDefaultsToTheRecursiveChunker(t *testing.T) {
	eng := registerForIngest(t)
	ctx := weave.WithTenant(context.Background(), "t1")
	col := &collection.Collection{Name: "c"}
	if err := eng.CreateCollection(ctx, col); err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if col.ChunkSize != 512 || col.ChunkOverlap != 50 {
		t.Errorf("collection chunk size %d overlap %d, want weave's defaults 512 and 50", col.ChunkSize, col.ChunkOverlap)
	}
	if _, err := eng.Ingest(ctx, &engine.IngestInput{
		CollectionID: col.ID, Title: "t", Content: "refunds are issued within thirty days",
		Metadata: map[string]string{"kind": "report", "project": "p1"},
	}); err != nil {
		t.Fatalf("ingest with no chunker set: %v", err)
	}
	hits, err := eng.Retrieve(ctx, "refunds thirty days", engine.WithCollection(col.ID))
	if err != nil || len(hits) == 0 {
		t.Fatalf("retrieve: %v, %d hits", err, len(hits))
	}
	if md := hits[0].Chunk.Metadata; md["kind"] != "report" || md["project"] != "p1" {
		t.Errorf("hit metadata %v, want the document's kind and project", md)
	}
	if got := eng.Components().Chunker.Kind; got != "recursive" {
		t.Errorf("chunker kind %q, want recursive", got)
	}
}

type noopChunker struct{}

func (noopChunker) Chunk(context.Context, string, *chunker.Options) ([]chunker.ChunkResult, error) {
	return nil, nil
}

func TestExtensionKeepsAChunkerTheHostSet(t *testing.T) {
	eng := registerForIngest(t, WithEngineOption(engine.WithChunker(noopChunker{})))
	if got := eng.Components().Chunker.Kind; got == "recursive" {
		t.Errorf("chunker kind %q, want the host's own chunker kept", got)
	}
}
