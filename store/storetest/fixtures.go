package storetest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

func mustCollection(t *testing.T, s store.Store, name, tenant string) *collection.Collection {
	t.Helper()
	col := &collection.Collection{
		ID:             id.NewCollectionID(),
		Name:           name,
		Description:    "about " + name,
		TenantID:       tenant,
		AppID:          "app-1",
		EmbeddingModel: "test-embed",
		EmbeddingDims:  8,
		ChunkStrategy:  "recursive",
		ChunkSize:      64,
		ChunkOverlap:   8,
		Metadata:       map[string]string{"team": "search"},
	}
	if err := s.CreateCollection(context.Background(), col); err != nil {
		t.Fatalf("create collection %q: %v", name, err)
	}
	return col
}

func mustDocument(t *testing.T, s store.Store, col *collection.Collection, title string, state document.State) *document.Document {
	t.Helper()
	docID := id.NewDocumentID()
	doc := &document.Document{
		ID:            docID,
		CollectionID:  col.ID,
		TenantID:      col.TenantID,
		Title:         title,
		Source:        "test://" + title,
		SourceType:    "text/plain",
		ContentHash:   "sha-" + docID.String(),
		ContentLength: 120,
		Metadata:      map[string]string{"lang": "en"},
		State:         state,
	}
	if state == document.StateFailed {
		doc.Error = "embed: quota exceeded"
	}
	if err := s.CreateDocument(context.Background(), doc); err != nil {
		t.Fatalf("create document %q: %v", title, err)
	}
	return doc
}

// mustChunks writes n chunks for doc with distinct offsets, token counts and
// metadata so a round trip that drops any of them is caught.
func mustChunks(t *testing.T, s store.Store, doc *document.Document, n int) []*chunk.Chunk {
	t.Helper()
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
	if err := s.CreateChunkBatch(context.Background(), chunks); err != nil {
		t.Fatalf("create chunks for %q: %v", doc.Title, err)
	}
	return chunks
}

func collectionIDs(cols []*collection.Collection) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.ID.String()
	}
	return out
}

func documentIDs(docs []*document.Document) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID.String()
	}
	return out
}

func chunkIDs(chs []*chunk.Chunk) []string {
	out := make([]string, len(chs))
	for i, c := range chs {
		out[i] = c.ID.String()
	}
	return out
}

func sameSet(t *testing.T, label string, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, ",") != strings.Join(w, ",") {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}

func sameOrder(t *testing.T, label string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: got %v, want %v (order matters)", label, got, want)
	}
}

// assertSameInstant allows one millisecond of drift: Mongo stores
// milliseconds and Postgres microseconds.
func assertSameInstant(t *testing.T, label string, want, got time.Time) {
	t.Helper()
	if got.IsZero() {
		t.Errorf("%s: read back as zero time, want %v", label, want)
		return
	}
	if d := want.Sub(got); d > time.Millisecond || d < -time.Millisecond {
		t.Errorf("%s: got %v, want %v", label, got, want)
	}
}
