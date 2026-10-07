package contract

import (
	"context"
	"errors"
	"hash/fnv"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
	_ "github.com/xraph/grove/drivers/sqlitedriver/sqlitemigrate" // registers the sqlite migrate executor

	dashauth "github.com/xraph/forge/extensions/dashboard/auth"
	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/store/sqlite"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

// hashEmbedder is a deterministic bag-of-words embedder, so retrieval tests
// need no network. It mirrors the engine tests' helper.
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

// failingEmbedder fails every call, the way an embedding API out of quota
// does, after ingest has already written the document row.
type failingEmbedder struct{}

func (failingEmbedder) Embed(context.Context, []string) ([]embedder.EmbedResult, error) {
	return nil, errors.New("quota exceeded")
}

func (failingEmbedder) Dimensions() int { return 8 }

func openMemory(*testing.T) store.Store { return memory.New() }

func openSQLite(t *testing.T) store.Store {
	t.Helper()
	ctx := context.Background()
	drv := sqlitedriver.New()
	if err := drv.Open(ctx, filepath.Join(t.TempDir(), "weave.db")); err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := sqlite.New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	return s
}

// newDeps builds an engine on s with a memory vector store, the hashing
// embedder and the recursive chunker. opts are applied after those, so a
// test can replace the embedder.
func newDeps(t *testing.T, s store.Store, opts ...engine.Option) Deps {
	t.Helper()
	base := []engine.Option{
		engine.WithStore(s),
		engine.WithVectorStore(vsmemory.New()),
		engine.WithEmbedder(hashEmbedder{dims: 256}),
		engine.WithChunker(chunker.NewRecursiveChunker()),
	}
	e, err := engine.New(append(base, opts...)...)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	return Deps{Engine: e}
}

// forEachStore runs fn on the memory store and on a real SQLite store, so
// every handler is checked against at least one real backend.
func forEachStore(t *testing.T, fn func(t *testing.T, deps Deps)) {
	t.Helper()
	for _, b := range []struct {
		name string
		open func(*testing.T) store.Store
	}{{"memory", openMemory}, {"sqlite", openSQLite}} {
		t.Run(b.name, func(t *testing.T) { fn(t, newDeps(t, b.open(t))) })
	}
}

func codeOf(err error) dashcontract.ErrorCode {
	var ce *dashcontract.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func principal() dashcontract.Principal {
	return dashcontract.Principal{User: &dashauth.UserInfo{Subject: "operator"}}
}

func mustCollection(t *testing.T, deps Deps, name string) *collection.Collection {
	t.Helper()
	col := &collection.Collection{Name: name, ChunkSize: 512}
	if err := deps.Engine.CreateCollection(context.Background(), col); err != nil {
		t.Fatalf("create collection %q: %v", name, err)
	}
	return col
}

//nolint:revive // ctx second mirrors the engine tests' helper.
func mustIngest(t *testing.T, ctx context.Context, deps Deps, colID id.CollectionID, title, content string) id.DocumentID {
	t.Helper()
	res, err := deps.Engine.Ingest(ctx, &engine.IngestInput{CollectionID: colID, Title: title, Content: content})
	if err != nil {
		t.Fatalf("ingest %q: %v", title, err)
	}
	return res.DocumentID
}
