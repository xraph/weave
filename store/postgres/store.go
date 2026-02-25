// Package postgres provides a PostgreSQL implementation of the Weave
// composite store using grove ORM with grove migrations.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store is a PostgreSQL implementation of the composite Weave store.
type Store struct {
	db *grove.DB
	pg *pgdriver.PgDB
}

// New creates a new PostgreSQL store.
func New(db *grove.DB) *Store {
	return &Store{
		db: db,
		pg: pgdriver.Unwrap(db),
	}
}

// Migrate runs grove migrations for the Weave schema.
func (s *Store) Migrate(ctx context.Context) error {
	exec := pgmigrate.New(s.pg)
	for _, m := range Migrations.Migrations() {
		if m.Up != nil {
			if err := m.Up(ctx, exec); err != nil {
				return fmt.Errorf("weave: %w: %s: %w", weave.ErrMigrationFailed, m.Name, err)
			}
		}
	}
	return nil
}

// Ping verifies the database connection.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.Ping(ctx)
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

	_, err := s.pg.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create collection: %w", err)
	}
	return nil
}

func (s *Store) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	m := new(collectionModel)
	err := s.pg.NewSelect(m).Where("id = $1", colID.String()).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection: %w", err)
	}
	return collectionFromModel(m), nil
}

func (s *Store) GetCollectionByName(ctx context.Context, tenantID, name string) (*collection.Collection, error) {
	m := new(collectionModel)
	err := s.pg.NewSelect(m).
		Where("tenant_id = $1", tenantID).
		Where("name = $2", name).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection by name: %w", err)
	}
	return collectionFromModel(m), nil
}

func (s *Store) UpdateCollection(ctx context.Context, col *collection.Collection) error {
	col.UpdatedAt = time.Now().UTC()
	m := collectionToModel(col)

	res, err := s.pg.NewUpdate(m).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update collection: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("weave: update collection rows affected: %w", err)
	}
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	res, err := s.pg.NewDelete((*collectionModel)(nil)).
		Where("id = $1", colID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete collection: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("weave: delete collection rows affected: %w", err)
	}
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) ListCollections(ctx context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	var models []collectionModel
	q := s.pg.NewSelect(&models).OrderExpr("created_at ASC")

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

	_, err := s.pg.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create document: %w", err)
	}
	return nil
}

func (s *Store) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	m := new(documentModel)
	err := s.pg.NewSelect(m).Where("id = $1", docID.String()).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: get document: %w", err)
	}
	return documentFromModel(m), nil
}

func (s *Store) UpdateDocument(ctx context.Context, doc *document.Document) error {
	doc.UpdatedAt = time.Now().UTC()
	m := documentToModel(doc)

	res, err := s.pg.NewUpdate(m).WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update document: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("weave: update document rows affected: %w", err)
	}
	if n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	res, err := s.pg.NewDelete((*documentModel)(nil)).
		Where("id = $1", docID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete document: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("weave: delete document rows affected: %w", err)
	}
	if n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) ListDocuments(ctx context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	var models []documentModel
	q := s.pg.NewSelect(&models).OrderExpr("created_at ASC")

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = $1", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = $2", string(filter.State))
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
	q := s.pg.NewSelect((*documentModel)(nil))

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = $1", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = $2", string(filter.State))
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count documents: %w", err)
	}
	return count, nil
}

func (s *Store) DeleteDocumentsByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.pg.NewDelete((*documentModel)(nil)).
		Where("collection_id = $1", colID.String()).
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

	_, err := s.pg.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	return nil
}

func (s *Store) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	m := new(chunkModel)
	err := s.pg.NewSelect(m).Where("id = $1", chunkID.String()).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrChunkNotFound
		}
		return nil, fmt.Errorf("weave: get chunk: %w", err)
	}
	return chunkFromModel(m), nil
}

func (s *Store) ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	var models []chunkModel
	err := s.pg.NewSelect(&models).
		Where("document_id = $1", docID.String()).
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
	_, err := s.pg.NewDelete((*chunkModel)(nil)).
		Where("document_id = $1", docID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by document: %w", err)
	}
	return nil
}

func (s *Store) DeleteChunksByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.pg.NewDelete((*chunkModel)(nil)).
		Where("collection_id = $1", colID.String()).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by collection: %w", err)
	}
	return nil
}

func (s *Store) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	q := s.pg.NewSelect((*chunkModel)(nil))

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = $1", filter.CollectionID.String())
		}
		if filter.DocumentID.String() != "" {
			q = q.Where("document_id = $2", filter.DocumentID.String())
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count chunks: %w", err)
	}
	return count, nil
}

// isNoRows checks whether an error indicates no rows were found.
func isNoRows(err error) bool {
	return errors.Is(err, grove.ErrNoRows) || err.Error() == "no rows in result set"
}
