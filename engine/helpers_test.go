package engine_test

import (
	"context"
	"hash/fnv"
	"strings"
	"testing"

	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store/memory"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

// hashEmbedder is a deterministic bag-of-words embedder: each word adds one
// to a hashed dimension. Texts that share words score higher, so ranking
// tests can reason about order without a network call.
type hashEmbedder struct{ dims int }

func (h hashEmbedder) Embed(_ context.Context, texts []string) ([]embedder.EmbedResult, error) {
	out := make([]embedder.EmbedResult, len(texts))
	for i, text := range texts {
		v := make([]float32, h.dims)
		for _, w := range strings.Fields(strings.ToLower(text)) {
			f := fnv.New32a()
			_, _ = f.Write([]byte(w))
			v[f.Sum32()%uint32(h.dims)]++
		}
		out[i] = embedder.EmbedResult{Vector: v}
	}
	return out, nil
}

func (h hashEmbedder) Dimensions() int { return h.dims }

// testRig is an engine on memory stores, with the stores and embedder kept
// so a test can build retrievers on them or reach past the engine.
type testRig struct {
	Engine  *engine.Engine
	Store   *memory.Store
	Vectors *vsmemory.Store
	Embed   hashEmbedder
}

// rigOption builds an engine option from the rig's own parts, so a test can
// wire a retriever onto the same stores the engine uses.
type rigOption func(r *testRig) engine.Option

//nolint:unused // used by the engine tests that follow
func withOpt(o engine.Option) rigOption { return func(*testRig) engine.Option { return o } }

func newRig(t *testing.T, extra ...rigOption) *testRig {
	t.Helper()
	r := &testRig{Store: memory.New(), Vectors: vsmemory.New(), Embed: hashEmbedder{dims: 256}}
	opts := make([]engine.Option, 0, 4+len(extra))
	opts = append(opts,
		engine.WithStore(r.Store),
		engine.WithVectorStore(r.Vectors),
		engine.WithEmbedder(r.Embed),
		engine.WithChunker(chunker.NewRecursiveChunker()),
	)
	for _, f := range extra {
		opts = append(opts, f(r))
	}
	e, err := engine.New(opts...)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	r.Engine = e
	return r
}

func newTestEngine(t *testing.T, opts ...engine.Option) *engine.Engine {
	t.Helper()
	extra := make([]rigOption, len(opts))
	for i, o := range opts {
		extra[i] = withOpt(o)
	}
	return newRig(t, extra...).Engine
}

func mustTestCollection(t *testing.T, e *engine.Engine, name string) *collection.Collection {
	t.Helper()
	col := &collection.Collection{Name: name, ChunkSize: 512, ChunkOverlap: 0}
	if err := e.CreateCollection(context.Background(), col); err != nil {
		t.Fatalf("create collection %q: %v", name, err)
	}
	return col
}

// mustIngest ingests short content that the recursive chunker keeps as one
// chunk at size 512, and returns the document ID.
func mustIngest(t *testing.T, ctx context.Context, e *engine.Engine, colID id.CollectionID, title, content string) id.DocumentID { //nolint:revive // the signature is fixed by the slice 1 plan
	t.Helper()
	res, err := e.Ingest(ctx, &engine.IngestInput{CollectionID: colID, Title: title, Content: content})
	if err != nil {
		t.Fatalf("ingest %q: %v", title, err)
	}
	return res.DocumentID
}
