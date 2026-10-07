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

// testFilters pins each filter on its own and in combination, and pins every
// count to a list built from the same filter fields. On Postgres a filter that
// skipped an earlier one used to reference a placeholder with no argument and
// fail, which the old dashboard rendered as a zero. Search is literal: the regex
// metacharacters "(" and ".*" and the LIKE metacharacters "%", "_" and "\"
// each match only themselves.
func testFilters(t *testing.T, s store.Store) {
	ctx := context.Background()

	a := mustCollection(t, s, "alpha-col", "t1")
	b := mustCollection(t, s, "beta-col", "t1")
	report := mustDocument(t, s, a, "alpha report", document.StateReady)
	notes := mustDocument(t, s, a, "beta notes", document.StateFailed)
	draft := mustDocument(t, s, a, "gamma (draft)", document.StatePending)
	other := mustDocument(t, s, b, "alpha other", document.StateReady)
	percent := mustDocument(t, s, b, "50% off", document.StatePending)
	under := mustDocument(t, s, b, "a_b", document.StatePending)
	slash := mustDocument(t, s, b, `c:\dir`, document.StatePending)
	reportChunks := mustChunks(t, s, report, 3)
	mustChunks(t, s, notes, 2)

	// docs lists and counts with the same filter fields. The list must hold
	// exactly want, and the count must equal the length of that list.
	var none id.CollectionID // no collection filter

	docs := func(label string, collectionID id.CollectionID, state document.State, search string, want ...*document.Document) {
		t.Helper()
		got, err := s.ListDocuments(ctx, &document.ListFilter{CollectionID: collectionID, State: state, Search: search})
		if err != nil {
			t.Fatalf("%s: list: %v", label, err)
		}
		sameSet(t, label, documentIDs(got), documentIDs(want))
		n, err := s.CountDocuments(ctx, &document.CountFilter{CollectionID: collectionID, State: state, Search: search})
		if err != nil {
			t.Fatalf("%s: count: %v", label, err)
		}
		if n != int64(len(got)) {
			t.Errorf("%s: count %d, but the list holds %d", label, n, len(got))
		}
	}

	docs("state only", none, document.StateFailed, "", notes)
	docs("ready only", none, document.StateReady, "", report, other)
	docs("search only", none, "", "alpha", report, other)
	docs("collection and search", a.ID, "", "alpha", report)
	docs("collection, state and search", a.ID, document.StateReady, "alpha", report)

	docs("literal parenthesis", none, "", "(draft", draft)
	docs("literal dot star", none, "", ".*")
	docs("literal percent", none, "", "%", percent)
	docs("literal underscore", none, "", "_", under)
	docs("literal backslash", none, "", `\`, slash)

	cols := func(label, search string, want ...*collection.Collection) {
		t.Helper()
		got, err := s.ListCollections(ctx, &collection.ListFilter{Search: search})
		if err != nil {
			t.Fatalf("%s: list: %v", label, err)
		}
		sameSet(t, label, collectionIDs(got), collectionIDs(want))
		n, err := s.CountCollections(ctx, &collection.CountFilter{Search: search})
		if err != nil {
			t.Fatalf("%s: count: %v", label, err)
		}
		if n != int64(len(got)) {
			t.Errorf("%s: count %d, but the list holds %d", label, n, len(got))
		}
	}
	cols("collections search", "alpha", a)
	cols("collections literal dot star", ".*")
	cols("collections literal percent", "%")
	cols("collections literal underscore", "_")

	got, err := s.ListChunksByDocument(ctx, report.ID)
	if err != nil {
		t.Fatalf("list chunks by document: %v", err)
	}
	sameSet(t, "chunks by document", chunkIDs(got), chunkIDs(reportChunks))
	n, err := s.CountChunks(ctx, &chunk.CountFilter{DocumentID: report.ID})
	if err != nil {
		t.Fatalf("count chunks by document only: %v", err)
	}
	if n != int64(len(got)) {
		t.Errorf("count chunks by document only: count %d, but the list holds %d", n, len(got))
	}
}
