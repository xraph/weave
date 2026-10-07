package storetest

import (
	"context"
	"testing"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
)

// testFilters pins each filter on its own and in combination. On Postgres a
// filter that skipped an earlier one used to reference a placeholder with no
// argument and fail, which the old dashboard rendered as a zero.
func testFilters(t *testing.T, s store.Store) {
	ctx := context.Background()

	a := mustCollection(t, s, "alpha-col", "t1")
	b := mustCollection(t, s, "beta-col", "t1")
	report := mustDocument(t, s, a, "alpha report", document.StateReady)
	notes := mustDocument(t, s, a, "beta notes", document.StateFailed)
	draft := mustDocument(t, s, a, "gamma (draft)", document.StatePending)
	other := mustDocument(t, s, b, "alpha other", document.StateReady)
	reportChunks := mustChunks(t, s, report, 3)
	mustChunks(t, s, notes, 2)

	list := func(label string, f *document.ListFilter, want ...*document.Document) {
		t.Helper()
		got, err := s.ListDocuments(ctx, f)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		sameSet(t, label, documentIDs(got), documentIDs(want))
	}
	count := func(label string, f *document.CountFilter, want int64) {
		t.Helper()
		got, err := s.CountDocuments(ctx, f)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if got != want {
			t.Errorf("%s: got %d, want %d", label, got, want)
		}
	}

	list("state only", &document.ListFilter{State: document.StateFailed}, notes)
	list("search only", &document.ListFilter{Search: "alpha"}, report, other)
	list("collection and search", &document.ListFilter{CollectionID: a.ID, Search: "alpha"}, report)
	list("collection, state and search", &document.ListFilter{CollectionID: a.ID, State: document.StateReady, Search: "alpha"}, report)

	count("count state only", &document.CountFilter{State: document.StateReady}, 2)
	count("count search only", &document.CountFilter{Search: "alpha"}, 2)
	count("count all three", &document.CountFilter{CollectionID: a.ID, State: document.StateReady, Search: "alpha"}, 1)

	// Search is literal: regex and LIKE metacharacters match themselves.
	list("literal parenthesis", &document.ListFilter{Search: "(draft"}, draft)
	list("literal dot star", &document.ListFilter{Search: ".*"})
	cols, err := s.ListCollections(ctx, &collection.ListFilter{Search: ".*"})
	if err != nil {
		t.Fatalf("collections literal dot star: %v", err)
	}
	sameSet(t, "collections literal dot star", collectionIDs(cols), nil)

	n, err := s.CountChunks(ctx, &chunk.CountFilter{DocumentID: report.ID})
	if err != nil {
		t.Fatalf("count chunks by document only: %v", err)
	}
	if n != int64(len(reportChunks)) {
		t.Errorf("count chunks by document only: got %d, want %d", n, len(reportChunks))
	}
}
