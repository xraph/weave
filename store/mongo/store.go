// Package mongo provides a MongoDB implementation of the Weave
// composite store using Grove ORM with the mongodriver.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"
	"github.com/xraph/grove/drivers/mongodriver/mongomigrate"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

const (
	colCollections = "weave_collections"
	colDocuments   = "weave_documents"
	colChunks      = "weave_chunks"
)

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store is a MongoDB implementation of the composite Weave store.
type Store struct {
	db  *grove.DB
	mdb *mongodriver.MongoDB
}

// New creates a new MongoDB store.
func New(db *grove.DB) *Store {
	return &Store{
		db:  db,
		mdb: mongodriver.Unwrap(db),
	}
}

// Migrate runs grove migrations for the Weave schema (creates indexes).
func (s *Store) Migrate(ctx context.Context) error {
	exec := mongomigrate.New(s.mdb)
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

	_, err := s.mdb.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create collection: %w", err)
	}
	return nil
}

func (s *Store) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	m := new(collectionModel)
	err := s.mdb.NewFind(m).Filter(bson.M{"_id": colID.String()}).Scan(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection: %w", err)
	}
	return collectionFromModel(m)
}

func (s *Store) GetCollectionByName(ctx context.Context, tenantID, name string) (*collection.Collection, error) {
	m := new(collectionModel)
	err := s.mdb.NewFind(m).
		Filter(bson.M{"tenant_id": tenantID}).
		Filter(bson.M{"name": name}).
		Scan(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection by name: %w", err)
	}
	return collectionFromModel(m)
}

func (s *Store) UpdateCollection(ctx context.Context, col *collection.Collection) error {
	col.UpdatedAt = time.Now().UTC()
	m := collectionToModel(col)

	res, err := s.mdb.NewUpdate(m).Filter(bson.M{"_id": m.ID}).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update collection: %w", err)
	}
	if n := res.MatchedCount(); n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	res, err := s.mdb.NewDelete((*collectionModel)(nil)).
		Filter(bson.M{"_id": colID.String()}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete collection: %w", err)
	}
	if n := res.DeletedCount(); n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) ListCollections(ctx context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	var models []collectionModel
	q := s.mdb.NewFind(&models).Sort(bson.D{{Key: "created_at", Value: 1}})

	if filter != nil {
		if filter.Limit > 0 {
			q = q.Limit(int64(filter.Limit))
		}
		if filter.Offset > 0 {
			q = q.Skip(int64(filter.Offset))
		}
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("weave: list collections: %w", err)
	}

	result := make([]*collection.Collection, len(models))
	for i := range models {
		c, convErr := collectionFromModel(&models[i])
		if convErr != nil {
			return nil, convErr
		}
		result[i] = c
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

	_, err := s.mdb.NewInsert(m).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create document: %w", err)
	}
	return nil
}

func (s *Store) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	m := new(documentModel)
	err := s.mdb.NewFind(m).Filter(bson.M{"_id": docID.String()}).Scan(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: get document: %w", err)
	}
	return documentFromModel(m)
}

func (s *Store) UpdateDocument(ctx context.Context, doc *document.Document) error {
	doc.UpdatedAt = time.Now().UTC()
	m := documentToModel(doc)

	res, err := s.mdb.NewUpdate(m).Filter(bson.M{"_id": m.ID}).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update document: %w", err)
	}
	if n := res.MatchedCount(); n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	res, err := s.mdb.NewDelete((*documentModel)(nil)).
		Filter(bson.M{"_id": docID.String()}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete document: %w", err)
	}
	if n := res.DeletedCount(); n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) ListDocuments(ctx context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	var models []documentModel
	q := s.mdb.NewFind(&models).Sort(bson.D{{Key: "created_at", Value: 1}})

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Filter(bson.M{"collection_id": filter.CollectionID.String()})
		}
		if filter.State != "" {
			q = q.Filter(bson.M{"state": string(filter.State)})
		}
		if filter.Limit > 0 {
			q = q.Limit(int64(filter.Limit))
		}
		if filter.Offset > 0 {
			q = q.Skip(int64(filter.Offset))
		}
	}

	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("weave: list documents: %w", err)
	}

	result := make([]*document.Document, len(models))
	for i := range models {
		d, convErr := documentFromModel(&models[i])
		if convErr != nil {
			return nil, convErr
		}
		result[i] = d
	}
	return result, nil
}

func (s *Store) CountDocuments(ctx context.Context, filter *document.CountFilter) (int64, error) {
	q := s.mdb.NewFind((*documentModel)(nil))

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Filter(bson.M{"collection_id": filter.CollectionID.String()})
		}
		if filter.State != "" {
			q = q.Filter(bson.M{"state": string(filter.State)})
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count documents: %w", err)
	}
	return count, nil
}

func (s *Store) DeleteDocumentsByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.mdb.NewDelete((*documentModel)(nil)).
		Filter(bson.M{"collection_id": colID.String()}).
		Many().
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

	_, err := s.mdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	return nil
}

func (s *Store) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	m := new(chunkModel)
	err := s.mdb.NewFind(m).Filter(bson.M{"_id": chunkID.String()}).Scan(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, weave.ErrChunkNotFound
		}
		return nil, fmt.Errorf("weave: get chunk: %w", err)
	}
	return chunkFromModel(m)
}

func (s *Store) ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	var models []chunkModel
	err := s.mdb.NewFind(&models).
		Filter(bson.M{"document_id": docID.String()}).
		Sort(bson.D{{Key: "index", Value: 1}}).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("weave: list chunks by document: %w", err)
	}

	result := make([]*chunk.Chunk, len(models))
	for i := range models {
		c, convErr := chunkFromModel(&models[i])
		if convErr != nil {
			return nil, convErr
		}
		result[i] = c
	}
	return result, nil
}

func (s *Store) DeleteChunksByDocument(ctx context.Context, docID id.DocumentID) error {
	_, err := s.mdb.NewDelete((*chunkModel)(nil)).
		Filter(bson.M{"document_id": docID.String()}).
		Many().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by document: %w", err)
	}
	return nil
}

func (s *Store) DeleteChunksByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.mdb.NewDelete((*chunkModel)(nil)).
		Filter(bson.M{"collection_id": colID.String()}).
		Many().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by collection: %w", err)
	}
	return nil
}

func (s *Store) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	q := s.mdb.NewFind((*chunkModel)(nil))

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Filter(bson.M{"collection_id": filter.CollectionID.String()})
		}
		if filter.DocumentID.String() != "" {
			q = q.Filter(bson.M{"document_id": filter.DocumentID.String()})
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count chunks: %w", err)
	}
	return count, nil
}

// isNotFound checks whether an error indicates no documents were found.
func isNotFound(err error) bool {
	return errors.Is(err, mongo.ErrNoDocuments) ||
		errors.Is(err, grove.ErrNoRows)
}
