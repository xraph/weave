package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store/postgres"
)

// TestRowsWithoutATenantAreUnreachable writes a collection, a document and a
// chunk with tenant_id ”, the way the v1.8 dashboard did, straight into the
// tables. After the upgrade no tenant can see them: as t1 every list is empty
// and every get by those IDs is not-found, and with no tenant every call is
// refused. The rows stay in the table until an operator reassigns them.
func TestRowsWithoutATenantAreUnreachable(t *testing.T) {
	ctx, db := openMigrateDB(t)
	s := postgres.New(db)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pg := pgdriver.Unwrap(db)

	colID, docID, chunkID := id.NewCollectionID(), id.NewDocumentID(), id.NewChunkID()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO weave_collections (id, name, tenant_id, app_id) VALUES ($1, 'legacy', '', '')`, []any{colID.String()}},
		{`INSERT INTO weave_documents (id, collection_id, tenant_id, title, content_hash, state)
VALUES ($1, $2, '', 'legacy doc', 'sha-legacy', 'ready')`, []any{docID.String(), colID.String()}},
		{`INSERT INTO weave_chunks (id, document_id, collection_id, tenant_id, content, index)
VALUES ($1, $2, $3, '', 'legacy chunk', 0)`, []any{chunkID.String(), docID.String(), colID.String()}},
	} {
		if _, err := pg.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	t1 := weave.WithTenant(ctx, "t1")
	cols, err := s.ListCollections(t1, &collection.ListFilter{})
	if err != nil || len(cols) != 0 {
		t.Errorf("t1 lists collections: got %d rows, %v; want none", len(cols), err)
	}
	docs, err := s.ListDocuments(t1, &document.ListFilter{})
	if err != nil || len(docs) != 0 {
		t.Errorf("t1 lists documents: got %d rows, %v; want none", len(docs), err)
	}
	chs, err := s.ListChunks(t1, &chunk.ListFilter{CollectionID: colID})
	if err != nil || len(chs) != 0 {
		t.Errorf("t1 lists chunks: got %d rows, %v; want none", len(chs), err)
	}
	if chs, err = s.ListChunksByDocument(t1, docID); err != nil || len(chs) != 0 {
		t.Errorf("t1 lists chunks by document: got %d rows, %v; want none", len(chs), err)
	}
	for label, count := range map[string]func(context.Context) (int64, error){
		"collections": func(ctx context.Context) (int64, error) { return s.CountCollections(ctx, &collection.CountFilter{}) },
		"documents":   func(ctx context.Context) (int64, error) { return s.CountDocuments(ctx, &document.CountFilter{}) },
		"chunks":      func(ctx context.Context) (int64, error) { return s.CountChunks(ctx, &chunk.CountFilter{}) },
	} {
		if n, err := count(t1); err != nil || n != 0 {
			t.Errorf("t1 counts %s: got %d, %v; want 0", label, n, err)
		}
		if _, err := count(ctx); !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("count %s with no tenant: got %v, want ErrTenantRequired", label, err)
		}
	}
	if _, err := s.GetCollection(t1, colID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("t1 gets the legacy collection: got %v, want ErrCollectionNotFound", err)
	}
	if _, err := s.GetDocument(t1, docID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("t1 gets the legacy document: got %v, want ErrDocumentNotFound", err)
	}
	if _, err := s.GetChunk(t1, chunkID); !errors.Is(err, weave.ErrChunkNotFound) {
		t.Errorf("t1 gets the legacy chunk: got %v, want ErrChunkNotFound", err)
	}

	// Writes as t1 can't reach them either: updates relabelled to t1 and
	// deletes by ID are not-found, and bulk deletes remove nothing.
	legacyCol := &collection.Collection{ID: colID, Name: "taken", TenantID: "t1", Metadata: map[string]string{}}
	if err := s.UpdateCollection(t1, legacyCol); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("t1 updates the legacy collection: got %v, want ErrCollectionNotFound", err)
	}
	legacyDoc := &document.Document{ID: docID, CollectionID: colID, TenantID: "t1", Title: "taken", ContentHash: "sha-legacy", Metadata: map[string]string{}}
	if err := s.UpdateDocument(t1, legacyDoc); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("t1 updates the legacy document: got %v, want ErrDocumentNotFound", err)
	}
	if err := s.DeleteDocument(t1, docID); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("t1 deletes the legacy document: got %v, want ErrDocumentNotFound", err)
	}
	if err := s.DeleteCollection(t1, colID); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("t1 deletes the legacy collection: got %v, want ErrCollectionNotFound", err)
	}
	for name, del := range map[string]func() error{
		"DeleteChunksByDocument":      func() error { return s.DeleteChunksByDocument(t1, docID) },
		"DeleteChunksByCollection":    func() error { return s.DeleteChunksByCollection(t1, colID) },
		"DeleteDocumentsByCollection": func() error { return s.DeleteDocumentsByCollection(t1, colID) },
	} {
		if err := del(); err != nil {
			t.Errorf("t1 %s on the legacy rows: %v", name, err)
		}
	}
	// Nor can t1 attach rows to them.
	if err := s.CreateDocument(t1, &document.Document{ID: id.NewDocumentID(), CollectionID: colID, TenantID: "t1", ContentHash: "sha-new", State: document.StatePending}); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("t1 creates a document in the legacy collection: got %v, want ErrCollectionNotFound", err)
	}
	if err := s.CreateChunkBatch(t1, []*chunk.Chunk{{ID: id.NewChunkID(), DocumentID: docID, CollectionID: colID, TenantID: "t1", Content: "x"}}); !errors.Is(err, weave.ErrDocumentNotFound) {
		t.Errorf("t1 creates a chunk on the legacy document: got %v, want ErrDocumentNotFound", err)
	}

	// With no tenant at all, nothing answers, so "" can't be used to reach
	// the legacy rows either.
	untenanted := []struct {
		name string
		call func() error
	}{
		{"ListCollections", func() error { _, err := s.ListCollections(ctx, &collection.ListFilter{}); return err }},
		{"ListDocuments", func() error { _, err := s.ListDocuments(ctx, &document.ListFilter{}); return err }},
		{"ListChunks", func() error { _, err := s.ListChunks(ctx, &chunk.ListFilter{CollectionID: colID}); return err }},
		{"ListChunksByDocument", func() error { _, err := s.ListChunksByDocument(ctx, docID); return err }},
		{"GetCollection", func() error { _, err := s.GetCollection(ctx, colID); return err }},
		{"GetCollectionByName", func() error { _, err := s.GetCollectionByName(ctx, "", "legacy"); return err }},
		{"GetDocument", func() error { _, err := s.GetDocument(ctx, docID); return err }},
		{"GetChunk", func() error { _, err := s.GetChunk(ctx, chunkID); return err }},
		{"DeleteCollection", func() error { return s.DeleteCollection(ctx, colID) }},
		{"DeleteDocument", func() error { return s.DeleteDocument(ctx, docID) }},
		{"DeleteDocumentsByCollection", func() error { return s.DeleteDocumentsByCollection(ctx, colID) }},
		{"DeleteChunksByDocument", func() error { return s.DeleteChunksByDocument(ctx, docID) }},
		{"DeleteChunksByCollection", func() error { return s.DeleteChunksByCollection(ctx, colID) }},
		{"UpdateCollection", func() error {
			return s.UpdateCollection(ctx, &collection.Collection{ID: colID, Name: "legacy", Metadata: map[string]string{}})
		}},
		{"UpdateDocument", func() error {
			return s.UpdateDocument(ctx, &document.Document{ID: docID, CollectionID: colID, ContentHash: "sha-legacy", Metadata: map[string]string{}})
		}},
		{"CreateCollection", func() error {
			return s.CreateCollection(ctx, &collection.Collection{ID: id.NewCollectionID(), Name: "legacy-2", Metadata: map[string]string{}})
		}},
		{"CreateDocument", func() error {
			return s.CreateDocument(ctx, &document.Document{ID: id.NewDocumentID(), CollectionID: colID, ContentHash: "sha-2", Metadata: map[string]string{}})
		}},
		{"CreateChunkBatch", func() error {
			return s.CreateChunkBatch(ctx, []*chunk.Chunk{{ID: id.NewChunkID(), DocumentID: docID, CollectionID: colID, Content: "x"}})
		}},
	}
	for _, c := range untenanted {
		if err := c.call(); !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("%s with no tenant: got %v, want ErrTenantRequired", c.name, err)
		}
	}

	// The rows are still there, unchanged, for an operator to reassign, and
	// nothing else was written.
	for _, q := range []string{
		`SELECT count(*) FROM weave_collections WHERE tenant_id = '' AND name = 'legacy'`,
		`SELECT count(*) FROM weave_documents WHERE tenant_id = '' AND title = 'legacy doc'`,
		`SELECT count(*) FROM weave_chunks WHERE tenant_id = '' AND content = 'legacy chunk'`,
	} {
		var n int
		if err := pg.QueryRow(ctx, q).Scan(&n); err != nil {
			t.Fatalf("count legacy rows: %v", err)
		}
		if n != 1 {
			t.Errorf("%s: %d, want 1", q, n)
		}
	}
	for _, table := range []string{"weave_collections", "weave_documents", "weave_chunks"} {
		var n int
		if err := pg.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("%s holds %d rows, want only the legacy one", table, n)
		}
	}
}
