package engine_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/store/memory"
	"github.com/xraph/weave/vectorstore"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

const sharedText = "refunds are issued within thirty days of purchase"

func tenantCtx(tenant string) context.Context {
	return weave.WithTenant(context.Background(), tenant)
}

func TestEveryEngineCallRefusesAMissingTenant(t *testing.T) {
	r := newRig(t)
	t1 := tenantCtx("t1")
	col := mustTestCollection(t, r.Engine, "owned")
	docID := mustIngest(t, t1, r.Engine, col.ID, "refunds", sharedText)
	chunks, listErr := r.Engine.ListChunks(t1, &chunk.ListFilter{DocumentID: docID})
	if listErr != nil || len(chunks) == 0 {
		t.Fatalf("list chunks: %v, %d chunks", listErr, len(chunks))
	}
	chunkID := chunks[0].ID
	embedsBefore, vectorsBefore := r.Embed.Calls(), r.Vectors.Len()
	name := "renamed"

	calls := []struct {
		name string
		call func(ctx context.Context) error
	}{
		{"CreateCollection", func(ctx context.Context) error {
			return r.Engine.CreateCollection(ctx, &collection.Collection{Name: "new"})
		}},
		{"GetCollection", func(ctx context.Context) error { _, err := r.Engine.GetCollection(ctx, col.ID); return err }},
		{"GetCollectionByName", func(ctx context.Context) error { _, err := r.Engine.GetCollectionByName(ctx, col.Name); return err }},
		{"ListCollections", func(ctx context.Context) error { _, err := r.Engine.ListCollections(ctx, nil); return err }},
		{"UpdateCollection", func(ctx context.Context) error {
			_, err := r.Engine.UpdateCollection(ctx, col.ID, engine.CollectionUpdate{Name: &name})
			return err
		}},
		{"DeleteCollection", func(ctx context.Context) error { return r.Engine.DeleteCollection(ctx, col.ID) }},
		{"CollectionStats", func(ctx context.Context) error { _, err := r.Engine.CollectionStats(ctx, col.ID); return err }},
		{"Ingest", func(ctx context.Context) error {
			_, err := r.Engine.Ingest(ctx, &engine.IngestInput{CollectionID: col.ID, Content: "new words"})
			return err
		}},
		{"Ingest keyed", func(ctx context.Context) error {
			_, err := r.Engine.Ingest(ctx, &engine.IngestInput{CollectionID: col.ID, SourceKey: "a", Content: "new words"})
			return err
		}},
		{"DeleteBySource", func(ctx context.Context) error { return r.Engine.DeleteBySource(ctx, col.ID, "a") }},
		{"IngestBatch", func(ctx context.Context) error {
			_, err := r.Engine.IngestBatch(ctx, []*engine.IngestInput{{CollectionID: col.ID, Content: "new words"}})
			return err
		}},
		{"IngestBatch empty", func(ctx context.Context) error { _, err := r.Engine.IngestBatch(ctx, nil); return err }},
		{"Retrieve", func(ctx context.Context) error { _, err := r.Engine.Retrieve(ctx, "refunds"); return err }},
		{"HybridSearch", func(ctx context.Context) error {
			_, err := r.Engine.HybridSearch(ctx, "refunds", &engine.HybridSearchParams{Collections: []id.CollectionID{col.ID}})
			return err
		}},
		{"HybridSearch all", func(ctx context.Context) error { _, err := r.Engine.HybridSearch(ctx, "refunds", nil); return err }},
		{"RetrieveCompare", func(ctx context.Context) error {
			_, err := r.Engine.RetrieveCompare(ctx, "refunds", engine.CompareParams{})
			return err
		}},
		{"GetDocument", func(ctx context.Context) error { _, err := r.Engine.GetDocument(ctx, docID); return err }},
		{"ListDocuments", func(ctx context.Context) error {
			_, err := r.Engine.ListDocuments(ctx, &document.ListFilter{CollectionID: col.ID})
			return err
		}},
		{"DeleteDocument", func(ctx context.Context) error { return r.Engine.DeleteDocument(ctx, docID) }},
		{"ReindexCollection", func(ctx context.Context) error { return r.Engine.ReindexCollection(ctx, col.ID) }},
		{"CountCollections", func(ctx context.Context) error { _, err := r.Engine.CountCollections(ctx, nil); return err }},
		{"CountDocuments", func(ctx context.Context) error {
			_, err := r.Engine.CountDocuments(ctx, &document.CountFilter{CollectionID: col.ID})
			return err
		}},
		{"CountChunks", func(ctx context.Context) error {
			_, err := r.Engine.CountChunks(ctx, &chunk.CountFilter{CollectionID: col.ID})
			return err
		}},
		{"GetChunk", func(ctx context.Context) error { _, err := r.Engine.GetChunk(ctx, chunkID); return err }},
		{"ListChunks", func(ctx context.Context) error {
			_, err := r.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: docID})
			return err
		}},
		{"AssembleRefs", func(ctx context.Context) error {
			_, _, err := r.Engine.AssembleRefs(ctx, []engine.ChunkRef{{ChunkID: chunkID, Score: 1}}, engine.AssembleParams{})
			return err
		}},
		{"AssembleRefs empty", func(ctx context.Context) error {
			_, _, err := r.Engine.AssembleRefs(ctx, nil, engine.AssembleParams{})
			return err
		}},
	}
	for _, tc := range calls {
		for label, ctx := range map[string]context.Context{"background": context.Background(), "empty tenant": tenantCtx("")} {
			if err := tc.call(ctx); !errors.Is(err, weave.ErrTenantRequired) {
				t.Errorf("%s (%s): got %v, want ErrTenantRequired", tc.name, label, err)
			}
		}
	}

	if got := r.Embed.Calls(); got != embedsBefore {
		t.Errorf("embedder: %d calls during refused calls, want 0", got-embedsBefore)
	}
	if got := r.Vectors.Len(); got != vectorsBefore {
		t.Errorf("vectors: %d after refused calls, want %d", got, vectorsBefore)
	}
	if _, err := r.Engine.GetDocument(t1, docID); err != nil {
		t.Errorf("t1 document after refused calls: %v", err)
	}
	got, err := r.Engine.GetCollection(t1, col.ID)
	if err != nil || got.Name != "owned" {
		t.Errorf("t1 collection after refused calls: %+v, %v", got, err)
	}
}

func TestCreateCollectionTakesTheContextTenant(t *testing.T) {
	e := newTestEngine(t)
	col := &collection.Collection{Name: "mine"}
	if err := e.CreateCollection(tenantCtx("t1"), col); err != nil {
		t.Fatalf("create: %v", err)
	}
	if col.TenantID != "t1" {
		t.Errorf("tenant: got %q, want t1", col.TenantID)
	}
	if err := e.CreateCollection(tenantCtx("t1"), &collection.Collection{Name: "theirs", TenantID: "t2"}); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("create naming another tenant: got %v, want ErrInvalidArgument", err)
	}
	if n, err := e.CountCollections(tenantCtx("t2"), nil); err != nil || n != 0 {
		t.Errorf("t2 collections: %d, %v; want none", n, err)
	}
}

func TestIngestRefusesAnotherTenantsCollection(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "t1 only")

	_, err := r.Engine.Ingest(tenantCtx("t2"), &engine.IngestInput{CollectionID: col.ID, Content: sharedText})
	if !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Fatalf("ingest under t2: got %v, want ErrCollectionNotFound", err)
	}
	if n := r.Embed.Calls(); n != 0 {
		t.Errorf("embedder: %d calls, want 0", n)
	}
	if n := r.Vectors.Len(); n != 0 {
		t.Errorf("vectors: %d, want 0", n)
	}
	n, err := r.Engine.CountDocuments(tenantCtx("t1"), &document.CountFilter{CollectionID: col.ID})
	if err != nil || n != 0 {
		t.Errorf("t1 documents: %d, %v; want 0", n, err)
	}
	if n, err := r.Engine.CountDocuments(tenantCtx("t2"), nil); err != nil || n != 0 {
		t.Errorf("t2 documents: %d, %v; want 0", n, err)
	}
}

// t1Hits retrieves under t1 and returns how many hits came from doc.
func t1Hits(t *testing.T, e *engine.Engine, doc id.DocumentID) int {
	t.Helper()
	hits, err := e.Retrieve(tenantCtx("t1"), sharedText, engine.WithTopK(10))
	if err != nil {
		t.Fatalf("retrieve under t1: %v", err)
	}
	n := 0
	for _, h := range hits {
		if h.Chunk.DocumentID.String() == doc.String() && h.Hydrated {
			n++
		}
	}
	return n
}

func TestDeleteDocumentLeavesAnotherTenantsVectors(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "kept")
	doc := mustIngest(t, tenantCtx("t1"), r.Engine, col.ID, "refunds", sharedText)
	vectors := r.Vectors.Len()

	if err := r.Engine.DeleteDocument(tenantCtx("t2"), doc); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Fatalf("delete under t2: got %v, want ErrDocumentNotFound", err)
	}
	if got := r.Vectors.Len(); got != vectors {
		t.Errorf("vectors: %d after t2's delete, want %d", got, vectors)
	}
	if t1Hits(t, r.Engine, doc) == 0 {
		t.Error("retrieve under t1: the document's chunks are gone")
	}
}

func TestDeleteCollectionLeavesAnotherTenantsData(t *testing.T) {
	r := newRig(t)
	col := mustTestCollection(t, r.Engine, "kept")
	doc := mustIngest(t, tenantCtx("t1"), r.Engine, col.ID, "refunds", sharedText)
	vectors, embeds := r.Vectors.Len(), r.Embed.Calls()

	if err := r.Engine.DeleteCollection(tenantCtx("t2"), col.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("delete collection under t2: got %v, want ErrCollectionNotFound", err)
	}
	if err := r.Engine.ReindexCollection(tenantCtx("t2"), col.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("reindex under t2: got %v, want ErrCollectionNotFound", err)
	}
	if got := r.Vectors.Len(); got != vectors {
		t.Errorf("vectors: %d after t2's calls, want %d", got, vectors)
	}
	if got := r.Embed.Calls(); got != embeds {
		t.Errorf("embedder: %d calls during t2's reindex, want 0", got-embeds)
	}
	if _, err := r.Engine.GetDocument(tenantCtx("t1"), doc); err != nil {
		t.Errorf("t1 document: %v", err)
	}
	if t1Hits(t, r.Engine, doc) == 0 {
		t.Error("retrieve under t1: the collection's chunks are gone")
	}
}

// recordingVectors is a memory vector store that records every
// DeleteByMetadata filter it is given.
type recordingVectors struct {
	*vsmemory.Store
	mu      sync.Mutex
	deletes []map[string]string
}

func (v *recordingVectors) DeleteByMetadata(ctx context.Context, filter map[string]string) error {
	v.mu.Lock()
	v.deletes = append(v.deletes, maps.Clone(filter))
	v.mu.Unlock()
	return v.Store.DeleteByMetadata(ctx, filter)
}

func TestVectorDeletesCarryTheTenant(t *testing.T) {
	vs := &recordingVectors{Store: vsmemory.New()}
	e := newTestEngine(t, engine.WithVectorStore(vs))
	t1 := tenantCtx("t1")
	col := mustTestCollection(t, e, "deletes")
	doc := mustIngest(t, t1, e, col.ID, "refunds", sharedText)

	if err := e.ReindexCollection(t1, col.ID); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if err := e.DeleteDocument(t1, doc); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if err := e.DeleteCollection(t1, col.ID); err != nil {
		t.Fatalf("delete collection: %v", err)
	}

	if len(vs.deletes) != 3 {
		t.Fatalf("deletes: got %v, want one per call", vs.deletes)
	}
	want := []map[string]string{
		// Reindex replaces one document's vectors at a time.
		{"document_id": doc.String(), "tenant_id": "t1"},
		{"document_id": doc.String(), "tenant_id": "t1"},
		{"collection_id": col.ID.String(), "tenant_id": "t1"},
	}
	for i, w := range want {
		if !maps.Equal(vs.deletes[i], w) {
			t.Errorf("delete %d: filter %v, want %v", i, vs.deletes[i], w)
		}
	}
}

// labellingChunker emits the reserved vector keys in its own metadata, the
// way a careless custom chunker could.
type labellingChunker struct{ chunker.Chunker }

func (c labellingChunker) Chunk(ctx context.Context, text string, opts *chunker.Options) ([]chunker.ChunkResult, error) {
	out, err := c.Chunker.Chunk(ctx, text, opts)
	for i := range out {
		out[i].Metadata = map[string]string{
			"tenant_id": "t2", "collection_id": "col_other", "document_id": "doc_other", "chunk_index": "99", "section": "intro",
		}
	}
	return out, err
}

func TestRetrieveNeverCrossesTenants(t *testing.T) {
	r := newRig(t)
	mine := mustTestCollection(t, r.Engine, "shared name")
	theirs := mustCollectionAs(t, tenantCtx("t2"), r.Engine, "shared name")
	mustIngest(t, tenantCtx("t1"), r.Engine, mine.ID, "mine", sharedText)
	mustIngest(t, tenantCtx("t2"), r.Engine, theirs.ID, "theirs", sharedText)

	hits, err := r.Engine.Retrieve(tenantCtx("t1"), sharedText, engine.WithTopK(10))
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	onlyTenant(t, "retrieve", hits, "t1")

	hybrid, err := r.Engine.HybridSearch(tenantCtx("t1"), sharedText, &engine.HybridSearchParams{TopK: 10})
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	onlyTenant(t, "hybrid search", hybrid, "t1")

	// Naming t2's collection, or one that doesn't exist, under t1 is
	// refused before the query is embedded.
	t1 := tenantCtx("t1")
	embeds := r.Embed.Calls()
	for _, colID := range []id.CollectionID{theirs.ID, id.NewCollectionID()} {
		if got, gotErr := r.Engine.Retrieve(t1, sharedText, engine.WithCollection(colID)); !errors.Is(gotErr, weave.ErrCollectionNotFound) {
			t.Errorf("retrieve %s under t1: %d hits, %v; want ErrCollectionNotFound", colID, len(got), gotErr)
		}
		if got, gotErr := r.Engine.HybridSearch(t1, sharedText, &engine.HybridSearchParams{Collections: []id.CollectionID{mine.ID, colID}}); !errors.Is(gotErr, weave.ErrCollectionNotFound) {
			t.Errorf("hybrid search with %s under t1: %d hits, %v; want ErrCollectionNotFound", colID, len(got), gotErr)
		}
		if res, gotErr := r.Engine.RetrieveCompare(t1, sharedText, engine.CompareParams{CollectionID: colID}); !errors.Is(gotErr, weave.ErrCollectionNotFound) {
			t.Errorf("compare %s under t1: %+v, %v; want ErrCollectionNotFound", colID, res, gotErr)
		}
	}
	if got := r.Embed.Calls(); got != embeds {
		t.Errorf("embedder: %d calls during refused searches, want 0", got-embeds)
	}

	// Naming its own collection still works.
	own, err := r.Engine.Retrieve(t1, sharedText, engine.WithCollection(mine.ID), engine.WithTopK(10))
	if err != nil {
		t.Fatalf("retrieve own collection: %v", err)
	}
	onlyTenant(t, "retrieve own collection", own, "t1")
}

func TestChunkerMetadataCannotRelabelAVector(t *testing.T) {
	r := newRig(t, withOpt(engine.WithChunker(labellingChunker{chunker.NewRecursiveChunker()})))
	col := mustTestCollection(t, r.Engine, "labelled")
	doc := mustIngest(t, tenantCtx("t1"), r.Engine, col.ID, "labelled", sharedText)
	check := func(stage string) {
		t.Helper()
		other, err := r.Engine.Retrieve(tenantCtx("t2"), sharedText, engine.WithTopK(10))
		if err != nil {
			t.Fatalf("%s: retrieve under t2: %v", stage, err)
		}
		if len(other) != 0 {
			t.Errorf("%s: retrieve under t2: %d hits from t1's document, want none", stage, len(other))
		}
		hits, err := r.Engine.Retrieve(tenantCtx("t1"), sharedText, engine.WithTopK(10))
		if err != nil {
			t.Fatalf("%s: retrieve under t1: %v", stage, err)
		}
		onlyTenant(t, stage, hits, "t1")
		meta := hits[0].Chunk.Metadata
		want := map[string]string{
			"tenant_id": "t1", "collection_id": col.ID.String(), "document_id": doc.String(), "chunk_index": "0", "section": "intro",
		}
		for k, v := range want {
			if meta[k] != v {
				t.Errorf("%s: hit metadata[%q] = %q, want %q", stage, k, meta[k], v)
			}
		}
	}
	check("after ingest")
	if err := r.Engine.ReindexCollection(tenantCtx("t1"), col.ID); err != nil {
		t.Fatalf("reindex: %v", err)
	}
	check("after reindex")
}

// onlyTenant checks hits is exactly one hydrated hit of tenant.
func onlyTenant(t *testing.T, what string, hits []engine.ScoredChunk, tenant string) {
	t.Helper()
	if len(hits) != 1 {
		t.Errorf("%s: %d hits, want 1", what, len(hits))
	}
	for i, h := range hits {
		if !h.Hydrated || h.Orphaned || h.Chunk.TenantID != tenant {
			t.Errorf("%s: hit %d: tenant %q hydrated=%v orphaned=%v, want a hydrated %s hit", what, i, h.Chunk.TenantID, h.Hydrated, h.Orphaned, tenant)
		}
	}
}

func TestTenantOptionsMustMatchTheContext(t *testing.T) {
	r := newRig(t)
	mine := mustTestCollection(t, r.Engine, "mine")
	theirs := mustCollectionAs(t, tenantCtx("t2"), r.Engine, "theirs")
	mustIngest(t, tenantCtx("t1"), r.Engine, mine.ID, "mine", sharedText)
	mustIngest(t, tenantCtx("t2"), r.Engine, theirs.ID, "theirs", sharedText)
	embeds := r.Embed.Calls()

	t1 := tenantCtx("t1")
	if _, err := r.Engine.Retrieve(t1, sharedText, engine.WithTenantID("t2")); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("t1 context, WithTenantID(t2): got %v, want ErrInvalidArgument", err)
	}
	if _, err := r.Engine.Retrieve(t1, sharedText, engine.WithTenantFilter("")); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("t1 context, WithTenantFilter(\"\"): got %v, want ErrInvalidArgument", err)
	}
	if _, err := r.Engine.Retrieve(t1, sharedText, engine.WithTenantFilter("t2")); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("t1 context, WithTenantFilter(t2): got %v, want ErrInvalidArgument", err)
	}
	if _, err := r.Engine.RetrieveCompare(t1, sharedText, engine.CompareParams{Tenant: strPtr("t2")}); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("t1 context, CompareParams.Tenant t2: got %v, want ErrInvalidArgument", err)
	}
	if _, err := r.Engine.Retrieve(context.Background(), sharedText, engine.WithTenantFilter("")); !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("no context tenant, WithTenantFilter(\"\"): got %v, want ErrTenantRequired", err)
	}
	if got := r.Embed.Calls(); got != embeds {
		t.Errorf("embedder: %d calls during refused retrievals, want 0", got-embeds)
	}

	// With no context tenant the deprecated value becomes the tenant, and
	// the hits are read back under it.
	hits, err := r.Engine.Retrieve(context.Background(), sharedText, engine.WithTenantID("t1"), engine.WithTopK(10))
	if err != nil {
		t.Fatalf("no context tenant, WithTenantID(t1): %v", err)
	}
	onlyTenant(t, "WithTenantID(t1)", hits, "t1")

	hits, err = r.Engine.Retrieve(context.Background(), sharedText, engine.WithTenantFilter("t2"), engine.WithTopK(10))
	if err != nil {
		t.Fatalf("no context tenant, WithTenantFilter(t2): %v", err)
	}
	onlyTenant(t, "WithTenantFilter(t2)", hits, "t2")

	// A matching value is accepted.
	hits, err = r.Engine.Retrieve(t1, sharedText, engine.WithTenantID("t1"), engine.WithTenantFilter("t1"), engine.WithTopK(10))
	if err != nil {
		t.Fatalf("t1 context, matching options: %v", err)
	}
	onlyTenant(t, "matching options", hits, "t1")

	res, err := r.Engine.RetrieveCompare(context.Background(), sharedText, engine.CompareParams{Tenant: strPtr("t2"), TopK: 10})
	if err != nil {
		t.Fatalf("no context tenant, CompareParams.Tenant t2: %v", err)
	}
	if res.VectorMatches != 1 || len(res.Hits) != 1 || res.Hits[0].Chunk.TenantID != "t2" || !res.Hits[0].Hydrated {
		t.Errorf("compare as t2: vector matches %d, hits %+v; want one hydrated t2 hit", res.VectorMatches, res.Hits)
	}
}

// foreignStore answers GetCollection, GetDocument and GetChunk with t2's row
// whatever the context says, like a custom store that ignores the tenant.
type foreignStore struct {
	*memory.Store
}

var _ store.Store = foreignStore{}

func (s foreignStore) GetCollection(_ context.Context, colID id.CollectionID) (*collection.Collection, error) {
	return s.Store.GetCollection(tenantCtx("t2"), colID)
}

func (s foreignStore) GetDocument(_ context.Context, docID id.DocumentID) (*document.Document, error) {
	return s.Store.GetDocument(tenantCtx("t2"), docID)
}

func (s foreignStore) GetChunk(_ context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	return s.Store.GetChunk(tenantCtx("t2"), chunkID)
}

func TestCustomStoreRowFromAnotherTenantIsNotFound(t *testing.T) {
	inner := memory.New()
	e := newTestEngine(t, engine.WithStore(foreignStore{inner}))
	t2 := tenantCtx("t2")
	col := mustCollectionAs(t, t2, e, "theirs")
	doc := mustIngest(t, t2, e, col.ID, "theirs", sharedText)
	chunks, err := inner.ListChunksByDocument(t2, doc)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("t2 chunks: %d, %v", len(chunks), err)
	}

	t1 := tenantCtx("t1")
	if _, err := e.GetCollection(t1, col.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("get collection: got %v, want ErrCollectionNotFound", err)
	}
	if _, err := e.GetDocument(t1, doc); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("get document: got %v, want ErrDocumentNotFound", err)
	}
	if _, err := e.GetChunk(t1, chunks[0].ID); !errors.Is(err, weave.ErrChunkNotFound) {
		t.Errorf("get chunk: got %v, want ErrChunkNotFound", err)
	}
	if _, _, err := e.AssembleRefs(t1, []engine.ChunkRef{{ChunkID: chunks[0].ID}}, engine.AssembleParams{}); !errors.Is(err, weave.ErrChunkNotFound) {
		t.Errorf("assemble refs: got %v, want ErrChunkNotFound", err)
	}
	if _, err := e.CollectionStats(t1, col.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("collection stats: got %v, want ErrCollectionNotFound", err)
	}
	if _, err := e.Ingest(t1, &engine.IngestInput{CollectionID: col.ID, Content: "more words"}); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("ingest: got %v, want ErrCollectionNotFound", err)
	}
	if err := e.DeleteDocument(t1, doc); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("delete document: got %v, want ErrDocumentNotFound", err)
	}
	if err := e.DeleteCollection(t1, col.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("delete collection: got %v, want ErrCollectionNotFound", err)
	}
	if _, err := e.GetDocument(t2, doc); err != nil {
		t.Errorf("t2 document after t1's calls: %v", err)
	}
}

// strayVectors returns every entry it holds from Search, whatever the
// filter, like a custom vector store that ignores the tenant.
type strayVectors struct{ *vsmemory.Store }

func (v strayVectors) Search(ctx context.Context, vector []float32, opts *vectorstore.SearchOptions) ([]vectorstore.SearchResult, error) {
	return v.Store.Search(ctx, vector, &vectorstore.SearchOptions{TopK: opts.TopK, TenantKey: "t2"})
}

func TestRetrieveDropsAnotherTenantsHits(t *testing.T) {
	vs := strayVectors{vsmemory.New()}
	r := newRig(t, withOpt(engine.WithVectorStore(vs)))
	theirs := mustCollectionAs(t, tenantCtx("t2"), r.Engine, "theirs")
	doc := mustIngest(t, tenantCtx("t2"), r.Engine, theirs.ID, "theirs", sharedText)
	// Orphan t2's chunk too, so a hit can't be read back under any tenant.
	if err := r.Store.DeleteChunksByDocument(tenantCtx("t2"), doc); err != nil {
		t.Fatalf("delete chunks: %v", err)
	}

	hits, err := r.Engine.Retrieve(tenantCtx("t1"), sharedText, engine.WithTopK(10))
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("retrieve under t1: %d hits from t2's vectors (first %+v), want none", len(hits), hits[0].Chunk)
	}
	res, err := r.Engine.RetrieveCompare(tenantCtx("t1"), sharedText, engine.CompareParams{TopK: 10})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if len(res.Hits) != 0 || len(res.LeftOut) != 0 || res.VectorMatches != 0 || res.BestVectorScore != 0 {
		t.Errorf("compare under t1: hits %+v, left out %+v, %d vector matches (best %.3f); want nothing of t2's", res.Hits, res.LeftOut, res.VectorMatches, res.BestVectorScore)
	}
}

// strayRetriever returns another tenant's chunk with no ID and no metadata
// label, only its TenantID, as a custom retriever that ignores the tenant
// might.
type strayRetriever struct{}

func (strayRetriever) Retrieve(context.Context, string, *retriever.Options) ([]retriever.Result, error) {
	return []retriever.Result{{Chunk: &chunk.Chunk{TenantID: "t2", Content: "t2 secret text"}, Score: 0.9}}, nil
}

func TestRetrieveDropsAnUnlabelledHitFromAnotherTenant(t *testing.T) {
	e := newTestEngine(t, engine.WithRetriever(strayRetriever{}))
	hits, err := e.Retrieve(tenantCtx("t1"), sharedText)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("retrieve under t1: got %+v, want t2's hit dropped", hits[0].Chunk)
	}
}
