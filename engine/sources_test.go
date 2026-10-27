package engine_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/vectorstore"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

const (
	versionOne = "refunds are issued within thirty days, version one"
	versionTwo = "refunds are issued within thirty days, version two"
)

// ingestSource ingests content under sourceKey into colID as t1.
func ingestSource(t *testing.T, e *engine.Engine, colID id.CollectionID, sourceKey, content string) *engine.IngestResult {
	t.Helper()
	res, err := e.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: colID, SourceKey: sourceKey, Content: content})
	if err != nil {
		t.Fatalf("ingest %q under %q: %v", content, sourceKey, err)
	}
	return res
}

// vectorsOf returns every vector of t1 matching filter, read straight from
// the memory vector store.
func vectorsOf(t *testing.T, vs *vsmemory.Store, filter map[string]string) []vectorstore.SearchResult {
	t.Helper()
	f := map[string]string{"tenant_id": "t1"}
	for k, v := range filter {
		f[k] = v
	}
	query := make([]float32, 256)
	for i := range query {
		query[i] = 1
	}
	res, err := vs.Search(tenantCtx("t1"), query, &vectorstore.SearchOptions{TopK: 1000, Filter: f, TenantKey: "t1"})
	if err != nil {
		t.Fatalf("search vectors: %v", err)
	}
	return res
}

// retrieveDocs retrieves under t1 and returns the document ID of each hit,
// failing on a hit that is not hydrated.
func retrieveDocs(t *testing.T, e *engine.Engine, colID id.CollectionID) []string {
	t.Helper()
	hits, err := e.Retrieve(tenantCtx("t1"), versionOne, engine.WithCollection(colID), engine.WithTopK(50))
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		if !h.Hydrated || h.Orphaned {
			t.Errorf("hit %s: hydrated %v, orphaned %v; want a hydrated hit", h.Chunk.ID, h.Hydrated, h.Orphaned)
		}
		out = append(out, h.Chunk.DocumentID.String())
	}
	return out
}

// sourceDocs lists t1's documents of sourceKey in colID, in every state.
func sourceDocs(t *testing.T, e *engine.Engine, colID id.CollectionID, sourceKey string) []*document.Document {
	t.Helper()
	docs, err := e.ListDocuments(tenantCtx("t1"), &document.ListFilter{CollectionID: colID, SourceKey: sourceKey})
	if err != nil {
		t.Fatalf("list documents of %q: %v", sourceKey, err)
	}
	return docs
}

func TestIngestSameSourceSameContentIsUnchanged(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")

	first := ingestSource(t, r.Engine, col.ID, "a", versionOne)
	second, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionOne})
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if second.DocumentID.String() != first.DocumentID.String() || !second.Unchanged || second.State != document.StateReady {
		t.Errorf("second ingest: %+v; want %s, Unchanged, ready", second, first.DocumentID)
	}
	if second.ChunkCount != first.ChunkCount || len(second.Replaced) != 0 {
		t.Errorf("second ingest: %d chunks, replaced %v; want %d and none", second.ChunkCount, second.Replaced, first.ChunkCount)
	}
	if first.Unchanged {
		t.Error("first ingest: Unchanged, want a new document")
	}
	if n := r.Embed.Calls(); n != 1 {
		t.Errorf("embedder: %d calls, want 1", n)
	}
	if n, err := r.Engine.CountDocuments(tenantCtx("t1"), &document.CountFilter{CollectionID: col.ID}); err != nil || n != 1 {
		t.Errorf("documents: %d, %v; want 1", n, err)
	}
}

func TestIngestSameSourceNewContentReplaces(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")

	first := ingestSource(t, r.Engine, col.ID, "a", versionOne)
	second := ingestSource(t, r.Engine, col.ID, "a", versionTwo)

	if len(second.Replaced) != 1 || second.Replaced[0].String() != first.DocumentID.String() {
		t.Errorf("replaced: %v, want [%s]", second.Replaced, first.DocumentID)
	}
	if second.Unchanged || second.State != document.StateReady {
		t.Errorf("second ingest: %+v, want a new ready document", second)
	}
	if _, err := r.Engine.GetDocument(tenantCtx("t1"), first.DocumentID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("first version: got %v, want ErrDocumentNotFound", err)
	}
	if v := vectorsOf(t, r.Vectors, map[string]string{"document_id": first.DocumentID.String()}); len(v) != 0 {
		t.Errorf("first version: %d vectors left, want 0", len(v))
	}
	if n := r.Vectors.Len(); n != second.ChunkCount {
		t.Errorf("vectors: %d, want the second version's %d", n, second.ChunkCount)
	}
	docs := retrieveDocs(t, r.Engine, col.ID)
	if len(docs) == 0 {
		t.Fatal("retrieve: no hits, want the second version")
	}
	for _, d := range docs {
		if d != second.DocumentID.String() {
			t.Errorf("retrieve: hit from %s, want only %s", d, second.DocumentID)
		}
	}
	for _, v := range vectorsOf(t, r.Vectors, nil) {
		if v.Metadata["source_key"] != "a" {
			t.Errorf("vector %s: source_key %q, want a", v.ID, v.Metadata["source_key"])
		}
	}
}

// gatedVectors is a memory vector store that, once armed, stops inside the
// next Upsert after the entries are written, until release is closed.
type gatedVectors struct {
	*vsmemory.Store
	armed   atomic.Bool
	written chan struct{}
	release chan struct{}
}

func (g *gatedVectors) Upsert(ctx context.Context, entries []vectorstore.Entry) error {
	if err := g.Store.Upsert(ctx, entries); err != nil {
		return err
	}
	if g.armed.CompareAndSwap(true, false) {
		close(g.written)
		<-g.release
	}
	return nil
}

func TestReplaceNeverShowsTwoVersions(t *testing.T) {
	gate := &gatedVectors{written: make(chan struct{}), release: make(chan struct{})}
	r := newRig(t, func(r *testRig) engine.Option {
		gate.Store = r.Vectors
		return engine.WithVectorStore(gate)
	})
	col := mustTestCollection(t, r.Engine, "docs")
	first := ingestSource(t, r.Engine, col.ID, "a", versionOne)

	gate.armed.Store(true)
	done := make(chan *engine.IngestResult, 1)
	go func() {
		res, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionTwo})
		if err != nil {
			t.Errorf("second ingest: %v", err)
		}
		done <- res
	}()
	<-gate.written

	// The second version's vectors are written but not swapped in.
	if n := r.Vectors.Len(); n <= first.ChunkCount {
		t.Fatalf("vectors while blocked: %d, want the second version's too", n)
	}
	docs := retrieveDocs(t, r.Engine, col.ID)
	if len(docs) == 0 {
		t.Error("retrieve while blocked: no hits, want the first version")
	}
	for _, d := range docs {
		if d != first.DocumentID.String() {
			t.Errorf("retrieve while blocked: hit from %s, want only %s", d, first.DocumentID)
		}
	}

	close(gate.release)
	second := <-done
	if second == nil {
		t.FailNow()
	}
	docs = retrieveDocs(t, r.Engine, col.ID)
	if len(docs) == 0 {
		t.Error("retrieve after release: no hits, want the second version")
	}
	for _, d := range docs {
		if d != second.DocumentID.String() {
			t.Errorf("retrieve after release: hit from %s, want only %s", d, second.DocumentID)
		}
	}
}

// flakyEmbedder is a hash embedder that fails while fail is set.
type flakyEmbedder struct {
	*embedtest.Hash
	fail atomic.Bool
}

func (f *flakyEmbedder) Embed(ctx context.Context, texts []string) ([]embedder.EmbedResult, error) {
	if f.fail.Load() {
		return nil, errors.New("embedding provider unavailable")
	}
	return f.Hash.Embed(ctx, texts)
}

func TestFailedReplaceKeepsThePreviousVersion(t *testing.T) {
	flaky := &flakyEmbedder{Hash: embedtest.New(256)}
	r := newRig(t, withOpt(engine.WithEmbedder(flaky)))
	col := mustTestCollection(t, r.Engine, "docs")
	first := ingestSource(t, r.Engine, col.ID, "a", versionOne)

	flaky.fail.Store(true)
	if _, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionTwo}); err == nil {
		t.Fatal("ingest with a failing embedder: got nil, want an error")
	}
	flaky.fail.Store(false)
	doc, err := r.Engine.GetDocument(tenantCtx("t1"), first.DocumentID)
	if err != nil || doc.State != document.StateReady {
		t.Fatalf("first version after the failure: %+v, %v; want ready", doc, err)
	}
	for _, d := range retrieveDocs(t, r.Engine, col.ID) {
		if d != first.DocumentID.String() {
			t.Errorf("retrieve after the failure: hit from %s, want only %s", d, first.DocumentID)
		}
	}
	var failed *document.Document
	for _, d := range sourceDocs(t, r.Engine, col.ID, "a") {
		if d.State == document.StateFailed {
			failed = d
		}
	}
	if failed == nil {
		t.Fatal("no failed document of source a after the failure")
	}

	second := ingestSource(t, r.Engine, col.ID, "a", versionTwo)
	if len(second.Replaced) != 1 || second.Replaced[0].String() != first.DocumentID.String() {
		t.Errorf("replaced: %v, want [%s]", second.Replaced, first.DocumentID)
	}
	if _, err := r.Engine.GetDocument(tenantCtx("t1"), failed.ID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("failed version after a good ingest: got %v, want ErrDocumentNotFound", err)
	}
	if docs := sourceDocs(t, r.Engine, col.ID, "a"); len(docs) != 1 || docs[0].ID.String() != second.DocumentID.String() {
		t.Errorf("source a: %d documents, want only %s", len(docs), second.DocumentID)
	}
}

func TestSameContentUnderAnotherSourceIsDuplicate(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")
	ingestSource(t, r.Engine, col.ID, "a", versionOne)

	_, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "b", Content: versionOne})
	if !errors.Is(err, weave.ErrDuplicateDocument) {
		t.Errorf("same content under b: got %v, want ErrDuplicateDocument", err)
	}
	_, err = r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, Content: versionOne})
	if !errors.Is(err, weave.ErrDuplicateDocument) {
		t.Errorf("same content under no key: got %v, want ErrDuplicateDocument", err)
	}
	if n := r.Embed.Calls(); n != 1 {
		t.Errorf("embedder: %d calls, want 1", n)
	}
}

func TestDeleteBySourceRemovesEveryVersion(t *testing.T) {
	flaky := &flakyEmbedder{Hash: embedtest.New(256)}
	r := newRig(t, withOpt(engine.WithEmbedder(flaky)))
	col := mustTestCollection(t, r.Engine, "docs")
	ingestSource(t, r.Engine, col.ID, "a", versionOne)
	flaky.fail.Store(true)
	if _, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionTwo}); err == nil {
		t.Fatal("ingest with a failing embedder: got nil, want an error")
	}
	flaky.fail.Store(false)
	kept := ingestSource(t, r.Engine, col.ID, "b", "shipping takes five working days")
	if n := len(sourceDocs(t, r.Engine, col.ID, "a")); n != 2 {
		t.Fatalf("source a before the delete: %d documents, want a ready and a failed one", n)
	}

	if err := r.Engine.DeleteBySource(tenantCtx("t2"), col.ID, "a"); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("delete under t2: got %v, want ErrCollectionNotFound", err)
	}
	if n := len(sourceDocs(t, r.Engine, col.ID, "a")); n != 2 {
		t.Errorf("source a after t2's delete: %d documents, want 2", n)
	}

	if err := r.Engine.DeleteBySource(tenantCtx("t1"), col.ID, "a"); err != nil {
		t.Fatalf("delete source a: %v", err)
	}
	if docs := sourceDocs(t, r.Engine, col.ID, "a"); len(docs) != 0 {
		t.Errorf("source a after the delete: %d documents, want none", len(docs))
	}
	if v := vectorsOf(t, r.Vectors, map[string]string{"source_key": "a"}); len(v) != 0 {
		t.Errorf("source a after the delete: %d vectors, want none", len(v))
	}
	for _, d := range retrieveDocs(t, r.Engine, col.ID) {
		if d != kept.DocumentID.String() {
			t.Errorf("retrieve after the delete: hit from %s, want only source b's %s", d, kept.DocumentID)
		}
	}
	if docs := sourceDocs(t, r.Engine, col.ID, "b"); len(docs) != 1 || docs[0].ID.String() != kept.DocumentID.String() {
		t.Errorf("source b after deleting a: %d documents, want only %s", len(docs), kept.DocumentID)
	}
	if err := r.Engine.DeleteBySource(tenantCtx("t1"), col.ID, "a"); err != nil {
		t.Errorf("second delete of source a: got %v, want nil", err)
	}
}

func TestSourceKeyValidation(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")

	for name, key := range map[string]string{"blank": "   ", "1025 bytes": strings.Repeat("k", 1025)} {
		_, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: key, Content: versionOne})
		if !errors.Is(err, weave.ErrInvalidArgument) {
			t.Errorf("ingest with a %s key: got %v, want ErrInvalidArgument", name, err)
		}
		if err := r.Engine.DeleteBySource(tenantCtx("t1"), col.ID, key); !errors.Is(err, weave.ErrInvalidArgument) {
			t.Errorf("delete with a %s key: got %v, want ErrInvalidArgument", name, err)
		}
	}
	if err := r.Engine.DeleteBySource(tenantCtx("t1"), col.ID, ""); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("delete with an empty key: got %v, want ErrInvalidArgument", err)
	}
	if n := r.Embed.Calls(); n != 0 {
		t.Errorf("embedder: %d calls, want 0", n)
	}
	if n, err := r.Engine.CountDocuments(tenantCtx("t1"), nil); err != nil || n != 0 {
		t.Errorf("documents: %d, %v; want none", n, err)
	}
	ingestSource(t, r.Engine, col.ID, strings.Repeat("k", 1024), versionOne)
}

func TestKeyedIngestChecksTheCollectionBeforeEmbedding(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")

	for name, colID := range map[string]id.CollectionID{"another tenant's": col.ID, "a missing": id.NewCollectionID()} {
		_, err := r.Engine.Ingest(tenantCtx("t2"), &engine.IngestInput{CollectionID: colID, SourceKey: "a", Content: versionOne})
		if !errors.Is(err, weave.ErrCollectionNotFound) {
			t.Errorf("keyed ingest into %s collection: got %v, want ErrCollectionNotFound", name, err)
		}
	}
	if _, err := r.Engine.Ingest(context.Background(), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionOne}); !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("keyed ingest without a tenant: got %v, want ErrTenantRequired", err)
	}
	if n := r.Embed.Calls(); n != 0 {
		t.Errorf("embedder: %d calls, want 0", n)
	}
	if n := r.Vectors.Len(); n != 0 {
		t.Errorf("vectors: %d, want 0", n)
	}
}

func TestReindexKeepsSourceKeyOnVectors(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")
	keyed := ingestSource(t, r.Engine, col.ID, "a", versionOne)
	plain := mustIngest(t, tenantCtx("t1"), r.Engine, col.ID, "plain", "shipping takes five working days")

	if err := r.Engine.ReindexCollection(tenantCtx("t1"), col.ID); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	vectors := vectorsOf(t, r.Vectors, map[string]string{"document_id": keyed.DocumentID.String()})
	if len(vectors) != keyed.ChunkCount {
		t.Fatalf("keyed document: %d vectors after reindex, want %d", len(vectors), keyed.ChunkCount)
	}
	for _, v := range vectors {
		if v.Metadata["source_key"] != "a" {
			t.Errorf("vector %s: source_key %q after reindex, want a", v.ID, v.Metadata["source_key"])
		}
	}
	for _, v := range vectorsOf(t, r.Vectors, map[string]string{"document_id": plain.String()}) {
		if _, ok := v.Metadata["source_key"]; ok {
			t.Errorf("unkeyed vector %s: carries source_key %q", v.ID, v.Metadata["source_key"])
		}
	}
}

func TestConcurrentSameSourceIngestKeepsOneReadyDocument(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")
	const n = 8

	ingestAll := func(content func(i int) string) []*engine.IngestResult {
		results := make([]*engine.IngestResult, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				res, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: content(i)})
				if err != nil {
					t.Errorf("ingest %d: %v", i, err)
				}
				results[i] = res
			})
		}
		wg.Wait()
		return results
	}
	readyDocs := func() []*document.Document {
		var ready []*document.Document
		for _, d := range sourceDocs(t, r.Engine, col.ID, "a") {
			if d.State == document.StateReady {
				ready = append(ready, d)
			}
		}
		return ready
	}

	results := ingestAll(func(int) string { return versionOne })
	unchanged := 0
	for _, res := range results {
		if res != nil && res.Unchanged {
			unchanged++
		}
	}
	if unchanged != n-1 {
		t.Errorf("same content: %d results Unchanged, want %d", unchanged, n-1)
	}
	if ready := readyDocs(); len(ready) != 1 {
		t.Errorf("same content: %d ready documents, want 1", len(ready))
	}
	if docs := sourceDocs(t, r.Engine, col.ID, "a"); len(docs) != 1 {
		t.Errorf("same content: %d documents, want 1", len(docs))
	} else if v := r.Vectors.Len(); v != docs[0].ChunkCount {
		t.Errorf("same content: %d vectors, want the ready version's %d", v, docs[0].ChunkCount)
	}

	ingestAll(func(i int) string { return fmt.Sprintf("refunds are issued within thirty days, edition %d", i) })
	ready := readyDocs()
	if len(ready) != 1 {
		t.Fatalf("new contents: %d ready documents, want 1", len(ready))
	}
	if docs := sourceDocs(t, r.Engine, col.ID, "a"); len(docs) != 1 {
		t.Errorf("new contents: %d documents left, want every other version purged", len(docs))
	}
	if v := r.Vectors.Len(); v != ready[0].ChunkCount {
		t.Errorf("new contents: %d vectors, want the ready version's %d", v, ready[0].ChunkCount)
	}
	docs := retrieveDocs(t, r.Engine, col.ID)
	if len(docs) == 0 {
		t.Error("retrieve: no hits, want the ready version")
	}
	for _, d := range docs {
		if d != ready[0].ID.String() {
			t.Errorf("retrieve: hit from %s, want only %s", d, ready[0].ID)
		}
	}
}

// gatedChunks is a memory store that, once armed, stops inside the next
// GetChunk after the row is read, until release is closed.
type gatedChunks struct {
	*memory.Store
	armed   atomic.Bool
	read    chan struct{}
	release chan struct{}
}

func (g *gatedChunks) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	ch, err := g.Store.GetChunk(ctx, chunkID)
	if g.armed.CompareAndSwap(true, false) {
		close(g.read)
		<-g.release
	}
	return ch, err
}

func TestSwapBetweenHydratesKeepsOneVersion(t *testing.T) {
	vectors := &gatedVectors{written: make(chan struct{}), release: make(chan struct{})}
	rows := &gatedChunks{read: make(chan struct{}), release: make(chan struct{})}
	r := newRig(t,
		func(r *testRig) engine.Option { vectors.Store = r.Vectors; return engine.WithVectorStore(vectors) },
		func(r *testRig) engine.Option { rows.Store = r.Store; return engine.WithStore(rows) },
	)
	col := mustTestCollection(t, r.Engine, "docs")
	first := ingestSource(t, r.Engine, col.ID, "a", versionOne)

	// The second version's vectors are written, its rows are not.
	vectors.armed.Store(true)
	ingested := make(chan *engine.IngestResult, 1)
	go func() {
		res, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionTwo})
		if err != nil {
			t.Errorf("second ingest: %v", err)
		}
		ingested <- res
	}()
	<-vectors.written

	// The search finds both versions' vectors. The first version's hit
	// (the closer match) is read back before the swap...
	rows.armed.Store(true)
	retrieved := make(chan []engine.ScoredChunk, 1)
	go func() {
		hits, err := r.Engine.Retrieve(tenantCtx("t1"), versionOne, engine.WithCollection(col.ID), engine.WithTopK(10))
		if err != nil {
			t.Errorf("retrieve: %v", err)
		}
		retrieved <- hits
	}()
	<-rows.read

	// ...and the second version's after it.
	close(vectors.release)
	second := <-ingested
	close(rows.release)
	hits := <-retrieved
	if second == nil {
		t.FailNow()
	}

	if len(hits) == 0 {
		t.Fatal("retrieve across the swap: no hits, want the second version")
	}
	for _, h := range hits {
		if h.Chunk.DocumentID.String() != second.DocumentID.String() {
			t.Errorf("retrieve across the swap: hit from %s (first is %s), want only %s", h.Chunk.DocumentID, first.DocumentID, second.DocumentID)
		}
	}
}

// stallingEmbedder is a hash embedder whose next call, once armed, waits
// for its context to end and fails with the context's error.
type stallingEmbedder struct {
	*embedtest.Hash
	armed   atomic.Bool
	entered chan struct{}
}

func (s *stallingEmbedder) Embed(ctx context.Context, texts []string) ([]embedder.EmbedResult, error) {
	if s.armed.CompareAndSwap(true, false) {
		close(s.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.Hash.Embed(ctx, texts)
}

// ctxStore is a memory store whose UpdateDocument refuses a context that
// has ended, as a database driver does.
type ctxStore struct{ *memory.Store }

func (c ctxStore) UpdateDocument(ctx context.Context, doc *document.Document) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Store.UpdateDocument(ctx, doc)
}

func TestRetryAfterCancelledIngestEmbedsAgain(t *testing.T) {
	stall := &stallingEmbedder{Hash: embedtest.New(256), entered: make(chan struct{})}
	r := newRig(t, withOpt(engine.WithEmbedder(stall)),
		func(r *testRig) engine.Option { return engine.WithStore(ctxStore{r.Store}) })
	col := mustTestCollection(t, r.Engine, "docs")

	ctx, cancel := context.WithCancel(tenantCtx("t1"))
	stall.armed.Store(true)
	failed := make(chan error, 1)
	go func() {
		_, err := r.Engine.Ingest(ctx, &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionOne})
		failed <- err
	}()
	<-stall.entered
	cancel()
	if err := <-failed; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ingest: got %v, want context.Canceled", err)
	}
	docs := sourceDocs(t, r.Engine, col.ID, "a")
	if len(docs) != 1 || docs[0].State != document.StateFailed {
		t.Fatalf("after the cancel: %d documents (%+v), want one failed", len(docs), docs)
	}

	embeds := stall.Calls()
	res := ingestSource(t, r.Engine, col.ID, "a", versionOne)
	if res.Unchanged || res.State != document.StateReady || res.DocumentID.String() == docs[0].ID.String() {
		t.Errorf("retry: %+v, want a new ready document", res)
	}
	if stall.Calls() == embeds {
		t.Error("retry: nothing embedded")
	}
	if v := vectorsOf(t, r.Vectors, map[string]string{"document_id": res.DocumentID.String(), "source_key": "a"}); len(v) != res.ChunkCount || len(v) == 0 {
		t.Errorf("retry: %d vectors, want %d", len(v), res.ChunkCount)
	}
	if docs := sourceDocs(t, r.Engine, col.ID, "a"); len(docs) != 1 {
		t.Errorf("after the retry: %d documents, want 1", len(docs))
	}
	for _, d := range retrieveDocs(t, r.Engine, col.ID) {
		if d != res.DocumentID.String() {
			t.Errorf("retrieve after the retry: hit from %s, want %s", d, res.DocumentID)
		}
	}
}

func TestStaleProcessingDocumentIsClaimed(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "docs")
	// What a process killed mid-ingest leaves behind.
	stale := &document.Document{
		Entity: weave.NewEntity(), ID: id.NewDocumentID(), CollectionID: col.ID, TenantID: "t1",
		ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(versionOne))), ContentLength: len(versionOne),
		SourceKey: "a", State: document.StateProcessing,
	}
	if err := r.Store.CreateDocument(tenantCtx("t1"), stale); err != nil {
		t.Fatalf("create stale document: %v", err)
	}

	res := ingestSource(t, r.Engine, col.ID, "a", versionOne)
	if res.Unchanged || res.State != document.StateReady || res.DocumentID.String() != stale.ID.String() {
		t.Errorf("ingest over a stale processing document: %+v, want %s made ready", res, stale.ID)
	}
	if n := r.Embed.Calls(); n != 1 {
		t.Errorf("embedder: %d calls, want 1", n)
	}
	if v := r.Vectors.Len(); v != res.ChunkCount || v == 0 {
		t.Errorf("vectors: %d, want %d", v, res.ChunkCount)
	}
	if again := ingestSource(t, r.Engine, col.ID, "a", versionOne); !again.Unchanged {
		t.Errorf("third ingest: %+v, want Unchanged", again)
	}
}

// barrierEmbedder is a hash embedder whose first n calls all wait until n
// calls have arrived, so n ingests are inside their embed at once.
type barrierEmbedder struct {
	*embedtest.Hash
	n       int64
	arrived atomic.Int64
	all     chan struct{}
}

func (b *barrierEmbedder) Embed(ctx context.Context, texts []string) ([]embedder.EmbedResult, error) {
	if c := b.arrived.Add(1); c <= b.n {
		if c == b.n {
			close(b.all)
		}
		<-b.all
	}
	return b.Hash.Embed(ctx, texts)
}

func TestConcurrentClaimsLeaveOneSetOfVectors(t *testing.T) {
	const n = 8
	barrier := &barrierEmbedder{Hash: embedtest.New(256), n: n, all: make(chan struct{})}
	r := newRig(t, withOpt(engine.WithEmbedder(barrier)))
	col := mustTestCollection(t, r.Engine, "docs")
	stale := &document.Document{
		Entity: weave.NewEntity(), ID: id.NewDocumentID(), CollectionID: col.ID, TenantID: "t1",
		ContentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(versionOne))), ContentLength: len(versionOne),
		SourceKey: "a", State: document.StateProcessing,
	}
	if err := r.Store.CreateDocument(tenantCtx("t1"), stale); err != nil {
		t.Fatalf("create stale document: %v", err)
	}

	// Every ingest claims the stale document and embeds; one swap wins.
	var unchanged atomic.Int64
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			res, err := r.Engine.Ingest(tenantCtx("t1"), &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: versionOne})
			if err != nil {
				t.Errorf("ingest %d: %v", i, err)
				return
			}
			if res.DocumentID.String() != stale.ID.String() || res.State != document.StateReady {
				t.Errorf("ingest %d: %+v, want %s ready", i, res, stale.ID)
			}
			if res.Unchanged {
				unchanged.Add(1)
			}
		})
	}
	wg.Wait()

	if got := unchanged.Load(); got != n-1 {
		t.Errorf("%d results Unchanged, want %d", got, n-1)
	}
	doc, err := r.Engine.GetDocument(tenantCtx("t1"), stale.ID)
	if err != nil || doc.State != document.StateReady {
		t.Fatalf("claimed document: %+v, %v; want ready", doc, err)
	}
	if v := r.Vectors.Len(); v != doc.ChunkCount {
		t.Errorf("vectors: %d, want the winning attempt's %d", v, doc.ChunkCount)
	}
	for _, d := range retrieveDocs(t, r.Engine, col.ID) {
		if d != stale.ID.String() {
			t.Errorf("retrieve: hit from %s, want %s", d, stale.ID)
		}
	}
}

// countingStore is a memory store that counts UpdateDocument calls.
type countingStore struct {
	*memory.Store
	updates atomic.Int64
}

func (c *countingStore) UpdateDocument(ctx context.Context, doc *document.Document) error {
	c.updates.Add(1)
	return c.Store.UpdateDocument(ctx, doc)
}

func TestKeyedIngestWritesNoStateBeforeTheSwap(t *testing.T) {
	counting := &countingStore{}
	r := newRig(t, func(r *testRig) engine.Option { counting.Store = r.Store; return engine.WithStore(counting) })
	col := mustTestCollection(t, r.Engine, "docs")

	// A late state write could undo a version another attempt has just made
	// ready, so the keyed path leaves the move to ready to the swap alone.
	ingestSource(t, r.Engine, col.ID, "a", versionOne)
	ingestSource(t, r.Engine, col.ID, "a", versionTwo)
	if n := counting.updates.Load(); n != 0 {
		t.Errorf("UpdateDocument: %d calls during keyed ingests, want 0", n)
	}
}
