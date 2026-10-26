package storetest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

func ptr(s string) *string { return &s }

// testTenantScoping gives t1 and t2 a collection, a ready document and two
// chunks each, then probes every read, update and delete as t1 with t2's IDs.
// Another tenant's row reads as missing, with the same not-found sentinel an
// unknown ID gets, so an ID never tells a caller what exists. Bulk deletes
// that name t2's IDs succeed and remove nothing. At the end t2 reads its rows
// back intact.
func testTenantScoping(t *testing.T, s store.Store) {
	t1, t2 := as("t1"), as("t2")

	c1 := mustCollection(t, s, "t1-col", "t1")
	c2 := mustCollection(t, s, "t2-col", "t2")
	d1 := mustDocument(t, s, c1, "t1 doc", document.StateReady)
	d2 := mustDocument(t, s, c2, "t2 doc", document.StateReady)
	ch1 := mustChunks(t, s, d1, 2)
	ch2 := mustChunks(t, s, d2, 2)

	// Lists and counts see only t1's rows, with and without a filter.
	cols, err := s.ListCollections(t1, &collection.ListFilter{})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	sameSet(t, "collections", collectionIDs(cols), collectionIDs([]*collection.Collection{c1}))
	if cols, err = s.ListCollections(t1, nil); err != nil {
		t.Fatalf("list collections with a nil filter: %v", err)
	}
	sameSet(t, "collections, nil filter", collectionIDs(cols), collectionIDs([]*collection.Collection{c1}))
	assertCount(t, "count collections", 1)(s.CountCollections(t1, &collection.CountFilter{}))

	docs, err := s.ListDocuments(t1, &document.ListFilter{})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	sameSet(t, "documents", documentIDs(docs), documentIDs([]*document.Document{d1}))
	if docs, err = s.ListDocuments(t1, &document.ListFilter{CollectionID: c2.ID}); err != nil {
		t.Fatalf("list documents of t2's collection: %v", err)
	}
	sameSet(t, "documents of t2's collection", documentIDs(docs), nil)
	assertCount(t, "count documents", 1)(s.CountDocuments(t1, &document.CountFilter{}))
	assertCount(t, "count documents of t2's collection", 0)(s.CountDocuments(t1, &document.CountFilter{CollectionID: c2.ID}))

	assertCount(t, "count chunks", int64(len(ch1)))(s.CountChunks(t1, &chunk.CountFilter{}))
	assertCount(t, "count chunks of t2's document", 0)(s.CountChunks(t1, &chunk.CountFilter{DocumentID: d2.ID}))
	chs, err := s.ListChunks(t1, &chunk.ListFilter{CollectionID: c1.ID})
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	sameSet(t, "chunks", chunkIDs(chs), chunkIDs(ch1))
	if chs, err = s.ListChunks(t1, &chunk.ListFilter{CollectionID: c2.ID}); err != nil {
		t.Fatalf("list chunks of t2's collection: %v", err)
	}
	sameSet(t, "chunks of t2's collection", chunkIDs(chs), nil)

	// Reads by ID: another tenant's row is the not-found sentinel.
	if _, err = s.GetCollection(t1, c2.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("get t2's collection: got %v, want ErrCollectionNotFound", err)
	}
	if _, err = s.GetCollectionByName(t1, "t1", c2.Name); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("get t2's collection by name: got %v, want ErrCollectionNotFound", err)
	}
	if _, err = s.GetDocument(t1, d2.ID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("get t2's document: got %v, want ErrDocumentNotFound", err)
	}
	if _, err = s.GetChunk(t1, ch2[0].ID); !errors.Is(err, weave.ErrChunkNotFound) {
		t.Errorf("get t2's chunk: got %v, want ErrChunkNotFound", err)
	}
	if chs, err = s.ListChunksByDocument(t1, d2.ID); err != nil {
		t.Errorf("list t2's chunks by document: %v", err)
	}
	sameSet(t, "t2's chunks by document", chunkIDs(chs), nil)

	// Updates by ID, both as stored and relabelled to the caller's tenant: a
	// store must check the stored row's tenant, not only the one it is sent.
	for _, label := range []string{"as stored", "relabelled t1"} {
		doc := *d2
		doc.Title = "taken over"
		col := *c2
		col.Description = "taken over"
		if label == "relabelled t1" {
			doc.TenantID, col.TenantID = "t1", "t1"
		}
		if err = s.UpdateDocument(t1, &doc); !errors.Is(err, weave.ErrDocumentNotFound) {
			t.Errorf("update t2's document, %s: got %v, want ErrDocumentNotFound", label, err)
		}
		if err = s.UpdateCollection(t1, &col); !errors.Is(err, weave.ErrCollectionNotFound) {
			t.Errorf("update t2's collection, %s: got %v, want ErrCollectionNotFound", label, err)
		}
	}

	// Deletes by ID are not-found; bulk deletes succeed and remove nothing.
	if err = s.DeleteDocument(t1, d2.ID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("delete t2's document: got %v, want ErrDocumentNotFound", err)
	}
	if err = s.DeleteCollection(t1, c2.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("delete t2's collection: got %v, want ErrCollectionNotFound", err)
	}
	if err = s.DeleteChunksByDocument(t1, d2.ID); err != nil {
		t.Errorf("delete t2's chunks by document: %v", err)
	}
	if err = s.DeleteChunksByCollection(t1, c2.ID); err != nil {
		t.Errorf("delete t2's chunks by collection: %v", err)
	}
	if err = s.DeleteDocumentsByCollection(t1, c2.ID); err != nil {
		t.Errorf("delete t2's documents by collection: %v", err)
	}

	// t2 still has everything, unchanged.
	gotCol, err := s.GetCollection(t2, c2.ID)
	if err != nil {
		t.Fatalf("t2 reads its collection back: %v", err)
	}
	if gotCol.Description != c2.Description || gotCol.TenantID != "t2" {
		t.Errorf("t2's collection changed: description %q, tenant %q", gotCol.Description, gotCol.TenantID)
	}
	gotDoc, err := s.GetDocument(t2, d2.ID)
	if err != nil {
		t.Fatalf("t2 reads its document back: %v", err)
	}
	if gotDoc.Title != "t2 doc" || gotDoc.TenantID != "t2" {
		t.Errorf("t2's document changed: title %q, tenant %q", gotDoc.Title, gotDoc.TenantID)
	}
	if chs, err = s.ListChunksByDocument(t2, d2.ID); err != nil {
		t.Fatalf("t2 lists its chunks: %v", err)
	}
	sameSet(t, "t2's chunks after t1's probes", chunkIDs(chs), chunkIDs(ch2))
	if _, err := s.GetCollectionByName(t2, "t2", c2.Name); err != nil {
		t.Errorf("t2 gets its collection by name: %v", err)
	}
}

// notScoped are the store.Store methods that take no tenant. Every other
// method must be in testTenantRequired's table.
var notScoped = []string{"Migrate", "Ping", "Close"}

// testTenantRequired calls every tenant-scoped store.Store method with no
// tenant on the context and expects ErrTenantRequired, aimed at rows that
// exist so a store that skipped the check would succeed instead. Then, as
// t1, it writes rows labelled t2 and looks a name up under t2, which are
// ErrInvalidArgument and write nothing.
func testTenantRequired(t *testing.T, s store.Store) {
	t1 := as("t1")
	col := mustCollection(t, s, "required", "t1")
	doc := mustDocument(t, s, col, "required doc", document.StateReady)
	chs := mustChunks(t, s, doc, 2)

	newCol := func(tenant string) *collection.Collection {
		return &collection.Collection{
			ID: id.NewCollectionID(), Name: "new-" + tenant, TenantID: tenant,
			ChunkStrategy: "recursive", ChunkSize: 64, Metadata: map[string]string{},
		}
	}
	newDoc := func(tenant string) *document.Document {
		docID := id.NewDocumentID()
		return &document.Document{
			ID: docID, CollectionID: col.ID, TenantID: tenant, Title: "new",
			ContentHash: "sha-" + docID.String(), State: document.StatePending, Metadata: map[string]string{},
		}
	}
	newChunk := func(tenant string) *chunk.Chunk {
		return &chunk.Chunk{
			ID: id.NewChunkID(), DocumentID: doc.ID, CollectionID: col.ID, TenantID: tenant,
			Content: "new chunk", Index: 9, Metadata: map[string]string{},
		}
	}
	renamed := *col
	renamed.Description = "renamed"
	retitled := *doc
	retitled.Title = "retitled"

	cases := []struct {
		method string
		call   func(ctx context.Context) error
	}{
		{"CreateCollection", func(ctx context.Context) error { return s.CreateCollection(ctx, newCol("t1")) }},
		{"GetCollection", func(ctx context.Context) error { _, err := s.GetCollection(ctx, col.ID); return err }},
		{"GetCollectionByName", func(ctx context.Context) error {
			_, err := s.GetCollectionByName(ctx, "t1", col.Name)
			return err
		}},
		{"UpdateCollection", func(ctx context.Context) error { return s.UpdateCollection(ctx, &renamed) }},
		{"DeleteCollection", func(ctx context.Context) error { return s.DeleteCollection(ctx, col.ID) }},
		{"ListCollections", func(ctx context.Context) error {
			_, err := s.ListCollections(ctx, &collection.ListFilter{})
			return err
		}},
		{"CountCollections", func(ctx context.Context) error {
			_, err := s.CountCollections(ctx, &collection.CountFilter{})
			return err
		}},
		{"CreateDocument", func(ctx context.Context) error { return s.CreateDocument(ctx, newDoc("t1")) }},
		{"GetDocument", func(ctx context.Context) error { _, err := s.GetDocument(ctx, doc.ID); return err }},
		{"UpdateDocument", func(ctx context.Context) error { return s.UpdateDocument(ctx, &retitled) }},
		{"DeleteDocument", func(ctx context.Context) error { return s.DeleteDocument(ctx, doc.ID) }},
		{"ListDocuments", func(ctx context.Context) error {
			_, err := s.ListDocuments(ctx, &document.ListFilter{})
			return err
		}},
		{"CountDocuments", func(ctx context.Context) error {
			_, err := s.CountDocuments(ctx, &document.CountFilter{})
			return err
		}},
		{"DeleteDocumentsByCollection", func(ctx context.Context) error { return s.DeleteDocumentsByCollection(ctx, col.ID) }},
		{"CreateChunkBatch", func(ctx context.Context) error { return s.CreateChunkBatch(ctx, []*chunk.Chunk{newChunk("t1")}) }},
		{"GetChunk", func(ctx context.Context) error { _, err := s.GetChunk(ctx, chs[0].ID); return err }},
		{"ListChunksByDocument", func(ctx context.Context) error { _, err := s.ListChunksByDocument(ctx, doc.ID); return err }},
		{"ListChunks", func(ctx context.Context) error {
			_, err := s.ListChunks(ctx, &chunk.ListFilter{CollectionID: col.ID})
			return err
		}},
		{"DeleteChunksByDocument", func(ctx context.Context) error { return s.DeleteChunksByDocument(ctx, doc.ID) }},
		{"DeleteChunksByCollection", func(ctx context.Context) error { return s.DeleteChunksByCollection(ctx, col.ID) }},
		{"CountChunks", func(ctx context.Context) error {
			_, err := s.CountChunks(ctx, &chunk.CountFilter{})
			return err
		}},
	}

	// The table covers every method of store.Store but notScoped, so a
	// method added to the interface fails here until it gets a case.
	covered := make([]string, 0, len(cases))
	for _, tc := range cases {
		covered = append(covered, tc.method)
	}
	iface := reflect.TypeFor[store.Store]()
	for i := range iface.NumMethod() {
		name := iface.Method(i).Name
		if !slices.Contains(notScoped, name) && !slices.Contains(covered, name) {
			t.Errorf("store.Store.%s has no case here", name)
		}
	}

	for _, tc := range cases {
		if err := tc.call(context.Background()); !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("%s with no tenant: got %v, want ErrTenantRequired", tc.method, err)
		}
		if err := tc.call(weave.WithTenant(context.Background(), "")); !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("%s with an empty tenant: got %v, want ErrTenantRequired", tc.method, err)
		}
	}

	// Nothing the untenanted calls aimed at changed.
	gotCol, err := s.GetCollection(t1, col.ID)
	if err != nil {
		t.Fatalf("t1 reads its collection back: %v", err)
	}
	if gotCol.Description != col.Description {
		t.Errorf("untenanted update changed the collection: description %q", gotCol.Description)
	}
	gotDoc, err := s.GetDocument(t1, doc.ID)
	if err != nil {
		t.Fatalf("t1 reads its document back: %v", err)
	}
	if gotDoc.Title != doc.Title {
		t.Errorf("untenanted update changed the document: title %q", gotDoc.Title)
	}
	got, err := s.ListChunks(t1, &chunk.ListFilter{CollectionID: col.ID})
	if err != nil {
		t.Fatalf("t1 lists its chunks: %v", err)
	}
	sameSet(t, "chunks after untenanted calls", chunkIDs(got), chunkIDs(chs))
	assertCount(t, "collections after untenanted calls", 1)(s.CountCollections(t1, &collection.CountFilter{}))
	assertCount(t, "documents after untenanted calls", 1)(s.CountDocuments(t1, &document.CountFilter{}))

	// As t1, writing rows labelled t2 is refused and writes nothing.
	other := newCol("t2")
	if err := s.CreateCollection(t1, other); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("create a t2 collection as t1: got %v, want ErrInvalidArgument", err)
	}
	if err := s.CreateDocument(t1, newDoc("t2")); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("create a t2 document as t1: got %v, want ErrInvalidArgument", err)
	}
	batch := []*chunk.Chunk{newChunk("t1"), newChunk("t2")}
	if err := s.CreateChunkBatch(t1, batch); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("create a batch holding a t2 chunk as t1: got %v, want ErrInvalidArgument", err)
	}
	for _, w := range []struct {
		tenant             string
		cols, docs, chunks int64
	}{{"t1", 1, 1, int64(len(chs))}, {"t2", 0, 0, 0}} {
		ctx := as(w.tenant)
		assertCount(t, w.tenant+" collections after refused writes", w.cols)(s.CountCollections(ctx, &collection.CountFilter{}))
		assertCount(t, w.tenant+" documents after refused writes", w.docs)(s.CountDocuments(ctx, &document.CountFilter{}))
		assertCount(t, w.tenant+" chunks after refused writes", w.chunks)(s.CountChunks(ctx, &chunk.CountFilter{}))
	}
	if _, err := s.GetCollection(as("t2"), other.ID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("refused t2 collection: got %v, want ErrCollectionNotFound", err)
	}

	// The tenant GetCollectionByName names must be the context tenant.
	for _, tenant := range []string{"t2", ""} {
		if _, err := s.GetCollectionByName(t1, tenant, col.Name); !errors.Is(err, weave.ErrInvalidArgument) {
			t.Errorf("get by name naming tenant %q as t1: got %v, want ErrInvalidArgument", tenant, err)
		}
	}
}

// testTenantFilterMustMatch pins the deprecated Tenant filter fields: a value
// equal to the context tenant works as if it were unset, and any other value,
// "" included, is ErrInvalidArgument.
func testTenantFilterMustMatch(t *testing.T, s store.Store) {
	t1 := as("t1")
	c1 := mustCollection(t, s, "t1-col", "t1")
	d1 := mustDocument(t, s, c1, "t1 doc", document.StateReady)
	ch1 := mustChunks(t, s, d1, 2)
	c2 := mustCollection(t, s, "t2-col", "t2")
	mustChunks(t, s, mustDocument(t, s, c2, "t2 doc", document.StateReady), 1)

	matching := ptr("t1")
	cols, err := s.ListCollections(t1, &collection.ListFilter{Tenant: matching})
	if err != nil {
		t.Fatalf("list collections, matching filter: %v", err)
	}
	sameSet(t, "collections, matching filter", collectionIDs(cols), collectionIDs([]*collection.Collection{c1}))
	assertCount(t, "count collections, matching filter", 1)(s.CountCollections(t1, &collection.CountFilter{Tenant: matching}))
	docs, err := s.ListDocuments(t1, &document.ListFilter{Tenant: matching})
	if err != nil {
		t.Fatalf("list documents, matching filter: %v", err)
	}
	sameSet(t, "documents, matching filter", documentIDs(docs), documentIDs([]*document.Document{d1}))
	assertCount(t, "count documents, matching filter", 1)(s.CountDocuments(t1, &document.CountFilter{Tenant: matching}))
	chs, err := s.ListChunks(t1, &chunk.ListFilter{Tenant: matching})
	if err != nil {
		t.Fatalf("list chunks, matching filter: %v", err)
	}
	sameSet(t, "chunks, matching filter", chunkIDs(chs), chunkIDs(ch1))
	assertCount(t, "count chunks, matching filter", int64(len(ch1)))(s.CountChunks(t1, &chunk.CountFilter{Tenant: matching}))

	for _, bad := range []string{"t2", ""} {
		f := ptr(bad)
		calls := []struct {
			name string
			call func() error
		}{
			{"ListCollections", func() error { _, err := s.ListCollections(t1, &collection.ListFilter{Tenant: f}); return err }},
			{"CountCollections", func() error { _, err := s.CountCollections(t1, &collection.CountFilter{Tenant: f}); return err }},
			{"ListDocuments", func() error { _, err := s.ListDocuments(t1, &document.ListFilter{Tenant: f}); return err }},
			{"CountDocuments", func() error { _, err := s.CountDocuments(t1, &document.CountFilter{Tenant: f}); return err }},
			{"ListChunks", func() error { _, err := s.ListChunks(t1, &chunk.ListFilter{Tenant: f}); return err }},
			{"CountChunks", func() error { _, err := s.CountChunks(t1, &chunk.CountFilter{Tenant: f}); return err }},
		}
		for _, c := range calls {
			if err := c.call(); !errors.Is(err, weave.ErrInvalidArgument) {
				t.Errorf("%s with Tenant %q as t1: got %v, want ErrInvalidArgument", c.name, bad, err)
			}
		}
	}
}

// assertCount returns a check for a (count, error) pair, so a count call can
// be passed to it directly.
func assertCount(t *testing.T, label string, want int64) func(int64, error) {
	t.Helper()
	return func(got int64, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}
		if got != want {
			t.Errorf("%s: got %d, want %d", label, got, want)
		}
	}
}

// testStalled pins UpdatedBefore, which the Overview uses to count
// documents stuck in processing. Three cutoffs fix the direction of the
// comparison: an inverted filter would give 2, 1, 0 instead of 0, 1, 2.
func testStalled(t *testing.T, s store.Store) {
	ctx := as("t1")
	col := mustCollection(t, s, "stalled", "t1")

	before := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	mustDocument(t, s, col, "old", document.StateProcessing)
	time.Sleep(20 * time.Millisecond)
	middle := time.Now().UTC()
	time.Sleep(20 * time.Millisecond)
	mustDocument(t, s, col, "fresh", document.StateProcessing)
	mustDocument(t, s, col, "done", document.StateReady)
	time.Sleep(20 * time.Millisecond)
	after := time.Now().UTC()

	cases := []struct {
		name   string
		cutoff time.Time
		want   int64
	}{
		{"before every write", before, 0},
		{"between old and fresh", middle, 1},
		{"after every write", after, 2},
		{"zero means no filter", time.Time{}, 2},
	}
	for _, tc := range cases {
		n, err := s.CountDocuments(ctx, &document.CountFilter{State: document.StateProcessing, UpdatedBefore: tc.cutoff})
		if err != nil {
			t.Fatalf("%s: count stalled: %v", tc.name, err)
		}
		if n != tc.want {
			t.Errorf("%s: count stalled: got %d, want %d", tc.name, n, tc.want)
		}
	}
}
