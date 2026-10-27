package storetest

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

// mustSourceDocument creates a document of source key in col, in state,
// owned by and created under col's tenant. Its content hash is unique, so
// many versions of one source can sit side by side.
func mustSourceDocument(t *testing.T, s store.Store, col *collection.Collection, key string, state document.State) *document.Document {
	t.Helper()
	docID := id.NewDocumentID()
	doc := &document.Document{
		ID:            docID,
		CollectionID:  col.ID,
		TenantID:      col.TenantID,
		Title:         key + " " + string(state),
		Source:        "test://" + key,
		SourceType:    "text/markdown",
		SourceKey:     key,
		ContentHash:   "sha-" + docID.String(),
		ContentLength: 120,
		Metadata:      map[string]string{"lang": "en"},
		State:         state,
	}
	if state == document.StateFailed {
		doc.Error = "embed: quota exceeded"
	}
	if err := s.CreateDocument(as(doc.TenantID), doc); err != nil {
		t.Fatalf("create document of %q in state %s: %v", key, state, err)
	}
	return doc
}

// chunksFor builds n chunks for doc without writing them.
func chunksFor(doc *document.Document, n int) []*chunk.Chunk {
	chunks := make([]*chunk.Chunk, n)
	for i := range chunks {
		chunks[i] = &chunk.Chunk{
			ID:           id.NewChunkID(),
			DocumentID:   doc.ID,
			CollectionID: doc.CollectionID,
			TenantID:     doc.TenantID,
			Content:      fmt.Sprintf("%s chunk %d", doc.Title, i),
			Index:        i,
			StartOffset:  i * 10,
			EndOffset:    i*10 + 12,
			TokenCount:   3 + i,
			Metadata:     map[string]string{"section": fmt.Sprint(i)},
		}
	}
	return chunks
}

// mustReplace runs ReplaceSourceDocument as doc's tenant and fails the test
// on an error.
func mustReplace(t *testing.T, s store.Store, doc *document.Document, chunks []*chunk.Chunk) []id.DocumentID {
	t.Helper()
	replaced, err := s.ReplaceSourceDocument(as(doc.TenantID), doc, chunks)
	if err != nil {
		t.Fatalf("replace source document %s: %v", doc.ID, err)
	}
	return replaced
}

// assertDocument reads docID back as tenant and checks its state and chunk
// count, and that its chunks are exactly want.
func assertDocument(t *testing.T, s store.Store, tenant string, docID id.DocumentID, state document.State, want []*chunk.Chunk) {
	t.Helper()
	ctx := as(tenant)
	got, err := s.GetDocument(ctx, docID)
	if err != nil {
		t.Fatalf("get document %s: %v", docID, err)
	}
	if got.State != state {
		t.Errorf("document %s: state %q, want %q", docID, got.State, state)
	}
	if state == document.StateReady && got.ChunkCount != len(want) {
		t.Errorf("document %s: chunk count %d, want %d", docID, got.ChunkCount, len(want))
	}
	chs, err := s.ListChunksByDocument(ctx, docID)
	if err != nil {
		t.Fatalf("list chunks of %s: %v", docID, err)
	}
	sameSet(t, fmt.Sprintf("chunks of %s (%s)", docID, state), chunkIDs(chs), chunkIDs(want))
}

func docIDStrings(ids []id.DocumentID) []string {
	out := make([]string, len(ids))
	for i, d := range ids {
		out[i] = d.String()
	}
	return out
}

// testSourceReplace swaps one source's document for a new version and back
// again. The first replace has nothing to supersede. The second supersedes
// v1, deletes its chunks and leaves v2 the only ready document of the
// source. A failed earlier version is superseded too.
func testSourceReplace(t *testing.T, s store.Store) {
	ctx := as("t1")
	col := mustCollection(t, s, "sources", "t1")
	const key = "docs/a.md"

	v1 := mustSourceDocument(t, s, col, key, document.StateProcessing)
	ch1 := chunksFor(v1, 2)
	if replaced := mustReplace(t, s, v1, ch1); len(replaced) != 0 {
		t.Errorf("first version replaced %v, want nothing", replaced)
	}
	assertDocument(t, s, "t1", v1.ID, document.StateReady, ch1)
	got, err := s.GetDocument(ctx, v1.ID)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if got.SourceKey != key {
		t.Errorf("v1 source key %q, want %q", got.SourceKey, key)
	}

	v2 := mustSourceDocument(t, s, col, key, document.StateProcessing)
	ch2 := chunksFor(v2, 3)
	replaced := mustReplace(t, s, v2, ch2)
	sameSet(t, "replaced by v2", docIDStrings(replaced), []string{v1.ID.String()})
	assertDocument(t, s, "t1", v1.ID, document.StateSuperseded, nil)
	assertDocument(t, s, "t1", v2.ID, document.StateReady, ch2)

	ready, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: col.ID, SourceKey: key, State: document.StateReady})
	if err != nil {
		t.Fatalf("list ready documents of the source: %v", err)
	}
	sameSet(t, "ready documents of the source", documentIDs(ready), documentIDs([]*document.Document{v2}))

	// A failed version is superseded along with the ready one, chunks and all.
	failed := mustSourceDocument(t, s, col, key, document.StateFailed)
	mustChunks(t, s, failed, 1)
	v3 := mustSourceDocument(t, s, col, key, document.StateProcessing)
	ch3 := chunksFor(v3, 1)
	replaced = mustReplace(t, s, v3, ch3)
	sameSet(t, "replaced by v3", docIDStrings(replaced), []string{v2.ID.String(), failed.ID.String()})
	assertDocument(t, s, "t1", v2.ID, document.StateSuperseded, nil)
	assertDocument(t, s, "t1", failed.ID, document.StateSuperseded, nil)
	assertDocument(t, s, "t1", v3.ID, document.StateReady, ch3)
}

// testSourceReplaceRefuses pins what ReplaceSourceDocument turns away, each
// before it writes anything: a document that isn't processing
// (ErrInvalidState), another tenant's document (ErrDocumentNotFound, as for
// an unknown ID), a document whose stored collection differs from the one
// passed (ErrDocumentNotFound), a stored source key that differs from the
// one passed (ErrInvalidArgument) and a document with no source key
// (ErrInvalidArgument).
func testSourceReplaceRefuses(t *testing.T, s store.Store) {
	t1 := as("t1")
	col := mustCollection(t, s, "refuses", "t1")

	// The live version of each key must survive a refused replace whole.
	for _, state := range []document.State{document.StatePending, document.StateReady, document.StateFailed, document.StateSuperseded} {
		key := "docs/" + string(state) + ".md"
		live := mustSourceDocument(t, s, col, key, document.StateProcessing)
		liveChunks := chunksFor(live, 2)
		mustReplace(t, s, live, liveChunks)
		// A source has one ready document, so the ready case replaces the
		// live version itself.
		doc, docChunks := live, liveChunks
		if state != document.StateReady {
			doc, docChunks = mustSourceDocument(t, s, col, key, state), nil
		}

		_, err := s.ReplaceSourceDocument(t1, doc, chunksFor(doc, 1))
		if !errors.Is(err, weave.ErrInvalidState) {
			t.Errorf("replace a %s document: got %v, want ErrInvalidState", state, err)
		}
		assertDocument(t, s, "t1", doc.ID, state, docChunks)
		assertDocument(t, s, "t1", live.ID, document.StateReady, liveChunks)
	}

	// Another tenant's processing document is not found, as stored and
	// relabelled to the caller's tenant.
	c2 := mustCollection(t, s, "theirs", "t2")
	d2 := mustSourceDocument(t, s, c2, "docs/theirs.md", document.StateProcessing)
	for _, label := range []string{"as stored", "relabelled t1"} {
		doc := *d2
		if label == "relabelled t1" {
			doc.TenantID = "t1"
		}
		chunks := chunksFor(&doc, 1)
		for _, ch := range chunks {
			ch.TenantID = "t1"
		}
		if _, err := s.ReplaceSourceDocument(t1, &doc, chunks); !errors.Is(err, weave.ErrDocumentNotFound) {
			t.Errorf("replace t2's document as t1, %s: got %v, want ErrDocumentNotFound", label, err)
		}
	}
	assertDocument(t, s, "t2", d2.ID, document.StateProcessing, nil)
	if _, err := s.ReplaceSourceDocument(t1, &document.Document{
		ID: id.NewDocumentID(), CollectionID: col.ID, TenantID: "t1", SourceKey: "docs/unknown.md",
	}, nil); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("replace an unknown document: got %v, want ErrDocumentNotFound", err)
	}

	// A processing version whose copy names another of the tenant's
	// collections is not found there, and one whose copy names another
	// source key is refused, both before anything is written: the stored
	// row decides, not the struct the caller passes.
	const key = "docs/v2.md"
	live := mustSourceDocument(t, s, col, key, document.StateProcessing)
	liveChunks := chunksFor(live, 2)
	mustReplace(t, s, live, liveChunks)
	v2 := mustSourceDocument(t, s, col, key, document.StateProcessing)
	other := mustCollection(t, s, "refuses-other", "t1")
	moved := *v2
	moved.CollectionID = other.ID
	if _, err := s.ReplaceSourceDocument(t1, &moved, chunksFor(&moved, 1)); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("replace with the document moved to another collection: got %v, want ErrDocumentNotFound", err)
	}
	rekeyed := *v2
	rekeyed.SourceKey = "docs/other.md"
	if _, err := s.ReplaceSourceDocument(t1, &rekeyed, chunksFor(&rekeyed, 1)); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("replace with another source key: got %v, want ErrInvalidArgument", err)
	}
	assertDocument(t, s, "t1", v2.ID, document.StateProcessing, nil)
	assertDocument(t, s, "t1", live.ID, document.StateReady, liveChunks)

	// A document with no source key is not source-managed.
	plain := mustDocument(t, s, col, "plain", document.StateProcessing)
	if _, err := s.ReplaceSourceDocument(t1, plain, chunksFor(plain, 1)); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("replace a document with no source key: got %v, want ErrInvalidArgument", err)
	}
	assertDocument(t, s, "t1", plain.ID, document.StateProcessing, nil)
}

// testSourceReplaceAtomic fails the chunk insert halfway through a replace
// and expects nothing of it to stay: the old version keeps its state and
// chunks, the new one stays processing with no chunks. A batch that repeats
// a chunk ID fails, and so does one that reuses a chunk ID of the version
// being superseded, because a chunk ID is never reused. A valid replace of
// the same document then goes through.
func testSourceReplaceAtomic(t *testing.T, s store.Store) {
	ctx := as("t1")
	col := mustCollection(t, s, "atomic", "t1")
	const key = "docs/a.md"
	v1 := mustSourceDocument(t, s, col, key, document.StateProcessing)
	ch1 := chunksFor(v1, 2)
	mustReplace(t, s, v1, ch1)
	v2 := mustSourceDocument(t, s, col, key, document.StateProcessing)

	repeated := chunksFor(v2, 3)
	repeated[2].ID = repeated[0].ID
	reused := chunksFor(v2, 2)
	reused[1].ID = ch1[0].ID
	for _, tc := range []struct {
		name  string
		batch []*chunk.Chunk
	}{{"a repeated chunk ID", repeated}, {"v1's chunk ID", reused}} {
		if _, err := s.ReplaceSourceDocument(ctx, v2, tc.batch); err == nil {
			t.Errorf("replace with %s: got nil, want an error", tc.name)
		}
		assertDocument(t, s, "t1", v1.ID, document.StateReady, ch1)
		assertDocument(t, s, "t1", v2.ID, document.StateProcessing, nil)
	}

	ch2 := chunksFor(v2, 2)
	replaced := mustReplace(t, s, v2, ch2)
	sameSet(t, "replaced after the failures", docIDStrings(replaced), []string{v1.ID.String()})
	assertDocument(t, s, "t1", v2.ID, document.StateReady, ch2)
	assertDocument(t, s, "t1", v1.ID, document.StateSuperseded, nil)
}

// testOneReadyDocumentPerSource replaces six versions of one source from six
// goroutines at once, the way replicas ingest the same source at boot. Every
// call succeeds, and the source ends with exactly one ready document and
// five superseded ones without chunks.
func testOneReadyDocumentPerSource(t *testing.T, s store.Store) {
	ctx := as("t1")
	col := mustCollection(t, s, "replicas", "t1")
	const key = "docs/boot.md"
	const n = 6

	docs := make([]*document.Document, n)
	batches := make([][]*chunk.Chunk, n)
	for i := range docs {
		docs[i] = mustSourceDocument(t, s, col, key, document.StateProcessing)
		batches[i] = chunksFor(docs[i], 2)
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range docs {
		wg.Go(func() {
			_, errs[i] = s.ReplaceSourceDocument(ctx, docs[i], batches[i])
		})
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("replace %d: %v", i, err)
		}
	}

	all, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: col.ID, SourceKey: key})
	if err != nil {
		t.Fatalf("list documents of the source: %v", err)
	}
	if len(all) != n {
		t.Fatalf("source has %d documents, want %d", len(all), n)
	}
	var ready, superseded int
	for i, doc := range docs {
		got, err := s.GetDocument(ctx, doc.ID)
		if err != nil {
			t.Fatalf("get version %d: %v", i, err)
		}
		switch got.State {
		case document.StateReady:
			ready++
			assertDocument(t, s, "t1", doc.ID, document.StateReady, batches[i])
		case document.StateSuperseded:
			superseded++
			assertDocument(t, s, "t1", doc.ID, document.StateSuperseded, nil)
		default:
			t.Errorf("version %d: state %q, want ready or superseded", i, got.State)
		}
	}
	if ready != 1 || superseded != n-1 {
		t.Errorf("source has %d ready and %d superseded documents, want 1 and %d", ready, superseded, n-1)
	}
}

// testSourceReplaceSharedChunkID runs two replaces of different sources at
// once whose batches share a chunk ID. One succeeds; the other fails whole,
// leaving its document processing with none of its chunks, so a duplicate ID
// never lands part of a batch.
func testSourceReplaceSharedChunkID(t *testing.T, s store.Store) {
	ctx := as("t1")
	col := mustCollection(t, s, "shared", "t1")
	docs := []*document.Document{
		mustSourceDocument(t, s, col, "docs/a.md", document.StateProcessing),
		mustSourceDocument(t, s, col, "docs/b.md", document.StateProcessing),
	}
	batches := [][]*chunk.Chunk{chunksFor(docs[0], 4), chunksFor(docs[1], 4)}
	batches[1][3].ID = batches[0][3].ID

	errs := make([]error, len(docs))
	var wg sync.WaitGroup
	for i := range docs {
		wg.Go(func() {
			_, errs[i] = s.ReplaceSourceDocument(ctx, docs[i], batches[i])
		})
	}
	wg.Wait()

	succeeded := 0
	for i, err := range errs {
		if err == nil {
			succeeded++
			assertDocument(t, s, "t1", docs[i].ID, document.StateReady, batches[i])
			continue
		}
		assertDocument(t, s, "t1", docs[i].ID, document.StateProcessing, nil)
	}
	if succeeded != 1 {
		t.Errorf("%d of 2 replaces sharing a chunk ID succeeded, want 1 (errors %v)", succeeded, errs)
	}
}

// testSourceKeyFilter pins ListFilter.SourceKey: an exact match, and ""
// lists every document whatever its key.
func testSourceKeyFilter(t *testing.T, s store.Store) {
	ctx := as("t1")
	col := mustCollection(t, s, "keys", "t1")
	x := mustSourceDocument(t, s, col, "x", document.StateReady)
	y := mustSourceDocument(t, s, col, "y", document.StateReady)
	none := mustDocument(t, s, col, "no key", document.StateReady)

	for _, tc := range []struct {
		key  string
		want []*document.Document
	}{
		{"x", []*document.Document{x}},
		{"y", []*document.Document{y}},
		{"x/", nil},
		{"", []*document.Document{x, y, none}},
	} {
		docs, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: col.ID, SourceKey: tc.key})
		if err != nil {
			t.Fatalf("list documents with source key %q: %v", tc.key, err)
		}
		sameSet(t, fmt.Sprintf("documents with source key %q", tc.key), documentIDs(docs), documentIDs(tc.want))
	}
	got, err := s.GetDocument(ctx, none.ID)
	if err != nil {
		t.Fatalf("get the document with no key: %v", err)
	}
	if got.SourceKey != "" {
		t.Errorf("document with no key reads back with source key %q", got.SourceKey)
	}
}
