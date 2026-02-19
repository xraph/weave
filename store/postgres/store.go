// Package postgres provides a PostgreSQL implementation of the Weave
// composite store using bun ORM with embedded SQL migrations.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store is a PostgreSQL implementation of the composite Weave store.
type Store struct {
	db *bun.DB
}

// New creates a new PostgreSQL store.
func New(db *bun.DB) *Store {
	return &Store{db: db}
}

// Migrate runs embedded SQL migrations.
func (s *Store) Migrate(ctx context.Context) error {
	files, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("weave: %w: %w", weave.ErrMigrationFailed, err)
	}

	for _, f := range files {
		data, err := migrations.ReadFile("migrations/" + f.Name())
		if err != nil {
			return fmt.Errorf("weave: %w: read %s: %w", weave.ErrMigrationFailed, f.Name(), err)
		}
		if _, err := s.db.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("weave: %w: exec %s: %w", weave.ErrMigrationFailed, f.Name(), err)
		}
	}
	return nil
}

// Ping verifies the database connection.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// ──────────────────────────────────────────────────
// Collection operations
// ──────────────────────────────────────────────────

func (s *Store) CreateCollection(ctx context.Context, col *collection.Collection) error {
	now := time.Now().UTC()
	col.CreatedAt = now
	col.UpdatedAt = now
	m := collectionToModel(col)

	_, err := s.db.NewInsert().Model(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create collection: %w", err)
	}
	return nil
}

func (s *Store) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	m := new(collectionModel)
	err := s.db.NewSelect().Model(m).Where("id = ?", colID.String()).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection: %w", err)
	}
	return collectionFromModel(m), nil
}

func (s *Store) GetCollectionByName(ctx context.Context, tenantID, name string) (*collection.Collection, error) {
	m := new(collectionModel)
	err := s.db.NewSelect().Model(m).
		Where("tenant_id = ?", tenantID).
		Where("name = ?", name).
		Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection by name: %w", err)
	}
	return collectionFromModel(m), nil
}

func (s *Store) UpdateCollection(ctx context.Context, col *collection.Collection) error {
	col.UpdatedAt = time.Now().UTC()
	m := collectionToModel(col)

	res, err := s.db.NewUpdate().Model(m).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update collection: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	res, err := s.db.NewDelete().
		Model((*collectionModel)(nil)).
		Where("id = ?", colID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete collection: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) ListCollections(ctx context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	var models []collectionModel
	q := s.db.NewSelect().Model(&models).OrderExpr("created_at ASC")

	if filter != nil {
		if filter.Limit > 0 {
			q = q.Limit(filter.Limit)
		}
		if filter.Offset > 0 {
			q = q.Offset(filter.Offset)
		}
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("weave: list collections: %w", err)
	}

	result := make([]*collection.Collection, len(models))
	for i := range models {
		result[i] = collectionFromModel(&models[i])
	}
	return result, nil
}

// ──────────────────────────────────────────────────
// Document operations
// ──────────────────────────────────────────────────

func (s *Store) CreateDocument(ctx context.Context, doc *document.Document) error {
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	m := documentToModel(doc)

	_, err := s.db.NewInsert().Model(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create document: %w", err)
	}
	return nil
}

func (s *Store) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	m := new(documentModel)
	err := s.db.NewSelect().Model(m).Where("id = ?", docID.String()).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: get document: %w", err)
	}
	return documentFromModel(m), nil
}

func (s *Store) UpdateDocument(ctx context.Context, doc *document.Document) error {
	doc.UpdatedAt = time.Now().UTC()
	m := documentToModel(doc)

	res, err := s.db.NewUpdate().Model(m).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update document: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	res, err := s.db.NewDelete().
		Model((*documentModel)(nil)).
		Where("id = ?", docID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete document: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) ListDocuments(ctx context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	var models []documentModel
	q := s.db.NewSelect().Model(&models).OrderExpr("created_at ASC")

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = ?", string(filter.State))
		}
		if filter.Limit > 0 {
			q = q.Limit(filter.Limit)
		}
		if filter.Offset > 0 {
			q = q.Offset(filter.Offset)
		}
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("weave: list documents: %w", err)
	}

	result := make([]*document.Document, len(models))
	for i := range models {
		result[i] = documentFromModel(&models[i])
	}
	return result, nil
}

func (s *Store) CountDocuments(ctx context.Context, filter *document.CountFilter) (int64, error) {
	q := s.db.NewSelect().Model((*documentModel)(nil))

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = ?", string(filter.State))
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count documents: %w", err)
	}
	return int64(count), nil
}

func (s *Store) DeleteDocumentsByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.db.NewDelete().
		Model((*documentModel)(nil)).
		Where("collection_id = ?", colID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete documents by collection: %w", err)
	}
	return nil
}

// ──────────────────────────────────────────────────
// Chunk operations
// ──────────────────────────────────────────────────

func (s *Store) CreateChunkBatch(ctx context.Context, chunks []*chunk.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}

	now := time.Now().UTC()
	models := make([]chunkModel, len(chunks))
	for i, ch := range chunks {
		ch.CreatedAt = now
		models[i] = *chunkToModel(ch)
	}

	_, err := s.db.NewInsert().Model(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	return nil
}

func (s *Store) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	m := new(chunkModel)
	err := s.db.NewSelect().Model(m).Where("id = ?", chunkID.String()).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrChunkNotFound
		}
		return nil, fmt.Errorf("weave: get chunk: %w", err)
	}
	return chunkFromModel(m), nil
}

func (s *Store) ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	var models []chunkModel
	err := s.db.NewSelect().Model(&models).
		Where("document_id = ?", docID.String()).
		OrderExpr("index ASC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("weave: list chunks by document: %w", err)
	}

	result := make([]*chunk.Chunk, len(models))
	for i := range models {
		result[i] = chunkFromModel(&models[i])
	}
	return result, nil
}

func (s *Store) DeleteChunksByDocument(ctx context.Context, docID id.DocumentID) error {
	_, err := s.db.NewDelete().
		Model((*chunkModel)(nil)).
		Where("document_id = ?", docID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by document: %w", err)
	}
	return nil
}

func (s *Store) DeleteChunksByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.db.NewDelete().
		Model((*chunkModel)(nil)).
		Where("collection_id = ?", colID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by collection: %w", err)
	}
	return nil
}

func (s *Store) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	q := s.db.NewSelect().Model((*chunkModel)(nil))

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.DocumentID.String() != "" {
			q = q.Where("document_id = ?", filter.DocumentID.String())
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count chunks: %w", err)
	}
	return int64(count), nil
}
