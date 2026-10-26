// Package postgres provides a PostgreSQL implementation of the Weave
// composite store using grove ORM with grove migrations.
package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"
	"github.com/xraph/grove/drivers/pgdriver/pgmigrate"
	"github.com/xraph/grove/migrate"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/sqllike"
	"github.com/xraph/weave/internal/tenancy"
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

// Migrate applies the pending migrations in Migrations through grove's
// orchestrator, which records each version in grove_migrations. Every Up is
// idempotent, so an install that ran them untracked is adopted in place.
//
// Grove v1.10.0 creates grove_migrations and grove_migration_locks before it
// takes the migration lock, so replicas booting together on a database
// without those tables race on the CREATE, and the losers fail with
// duplicate_table or duplicate_object. Migrate retries that failure a few
// times with a short jittered pause. By the next attempt the tables exist and
// grove's lock serialises the replicas.
func (s *Store) Migrate(ctx context.Context) error {
	var err error
	for attempt := 1; ; attempt++ {
		_, err = migrate.NewOrchestrator(pgmigrate.New(s.pg), Migrations).Migrate(ctx)
		if err == nil {
			return nil
		}
		if attempt == migrateAttempts || !isConcurrentCreate(err) {
			break
		}
		if waitErr := pause(ctx, migrateBackoff(attempt)); waitErr != nil {
			err = errors.Join(err, waitErr)
			break
		}
	}
	return fmt.Errorf("%w: %w", weave.ErrMigrationFailed, err)
}

// migrateAttempts bounds how many times Migrate runs the orchestrator when
// replicas race on grove's tracking tables.
const migrateAttempts = 5

// migrateBackoff returns the pause before the next attempt: a base that grows
// with each attempt plus up to as much again of jitter, so racing replicas
// spread out. Attempt 1 waits 50 to 100ms, attempt 4 at most 400ms.
func migrateBackoff(attempt int) time.Duration {
	base := time.Duration(attempt) * 50 * time.Millisecond
	return base + jitter(base)
}

// jitter returns a random duration in [0, limit).
func jitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return limit / 2
	}
	return time.Duration(n.Int64())
}

// pause waits for d or until ctx is done, whichever comes first.
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
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
	if _, err := tenancy.Owner(ctx, "collection", col.TenantID); err != nil {
		return err
	}
	now := time.Now().UTC()
	col.CreatedAt = now
	col.UpdatedAt = now
	m := collectionToModel(col)

	_, err := s.pg.NewInsert(m).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("weave: create collection: %w", weave.ErrCollectionAlreadyExists)
		}
		return fmt.Errorf("weave: create collection: %w", err)
	}
	return nil
}

func (s *Store) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	m := new(collectionModel)
	err = s.pg.NewSelect(m).Where("id = ?", colID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection: %w", err)
	}
	return collectionFromModel(m), nil
}

func (s *Store) GetCollectionByName(ctx context.Context, tenantID, name string) (*collection.Collection, error) {
	tenant, err := tenancy.Scope(ctx, &tenantID)
	if err != nil {
		return nil, err
	}
	m := new(collectionModel)
	err = s.pg.NewSelect(m).
		Where("tenant_id = ?", tenant).
		Where("name = ?", name).
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
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	if col.TenantID != tenant {
		return weave.ErrCollectionNotFound
	}
	col.UpdatedAt = time.Now().UTC()
	m := collectionToModel(col)

	res, err := s.pg.NewUpdate(m).WherePK().Where("tenant_id = ?", tenant).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("weave: update collection: %w", weave.ErrCollectionAlreadyExists)
		}
		return fmt.Errorf("weave: update collection: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("weave: update collection rows affected: %w", rowsErr)
	}
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	res, err := s.pg.NewDelete((*collectionModel)(nil)).
		Where("id = ?", colID.String()).
		Where("tenant_id = ?", tenant).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete collection: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("weave: delete collection rows affected: %w", rowsErr)
	}
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) ListCollections(ctx context.Context, filter *collection.ListFilter) ([]*collection.Collection, error) {
	tenant, err := tenancy.CollectionList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var models []collectionModel
	order := "created_at ASC, id ASC"
	if filter != nil && filter.SortDesc {
		order = "created_at DESC, id DESC"
	}
	q := s.pg.NewSelect(&models).OrderExpr(order).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.Search != "" {
			q = q.Where("name ILIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
		}
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

func (s *Store) CountCollections(ctx context.Context, filter *collection.CountFilter) (int64, error) {
	tenant, err := tenancy.CollectionCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	q := s.pg.NewSelect((*collectionModel)(nil)).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.Search != "" {
			q = q.Where("name ILIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count collections: %w", err)
	}
	return count, nil
}

// ──────────────────────────────────────────────────
// Document operations
// ──────────────────────────────────────────────────

func (s *Store) CreateDocument(ctx context.Context, doc *document.Document) error {
	if _, err := tenancy.Owner(ctx, "document", doc.TenantID); err != nil {
		return err
	}
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	m := documentToModel(doc)

	_, err := s.pg.NewInsert(m).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("weave: create document: %w", weave.ErrDuplicateDocument)
		}
		return fmt.Errorf("weave: create document: %w", err)
	}
	return nil
}

func (s *Store) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	m := new(documentModel)
	err = s.pg.NewSelect(m).Where("id = ?", docID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: get document: %w", err)
	}
	return documentFromModel(m), nil
}

func (s *Store) UpdateDocument(ctx context.Context, doc *document.Document) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	if doc.TenantID != tenant {
		return weave.ErrDocumentNotFound
	}
	doc.UpdatedAt = time.Now().UTC()
	m := documentToModel(doc)

	res, err := s.pg.NewUpdate(m).WherePK().Where("tenant_id = ?", tenant).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update document: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("weave: update document rows affected: %w", rowsErr)
	}
	if n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	res, err := s.pg.NewDelete((*documentModel)(nil)).
		Where("id = ?", docID.String()).
		Where("tenant_id = ?", tenant).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete document: %w", err)
	}
	n, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("weave: delete document rows affected: %w", rowsErr)
	}
	if n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) ListDocuments(ctx context.Context, filter *document.ListFilter) ([]*document.Document, error) {
	tenant, err := tenancy.DocumentList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var models []documentModel
	order := "created_at ASC, id ASC"
	if filter != nil && filter.SortDesc {
		order = "created_at DESC, id DESC"
	}
	q := s.pg.NewSelect(&models).OrderExpr(order).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = ?", string(filter.State))
		}
		if filter.Search != "" {
			q = q.Where("title ILIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
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
	tenant, err := tenancy.DocumentCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	q := s.pg.NewSelect((*documentModel)(nil)).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = ?", string(filter.State))
		}
		if filter.Search != "" {
			q = q.Where("title ILIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
		}
		if !filter.UpdatedBefore.IsZero() {
			q = q.Where("updated_at < ?", filter.UpdatedBefore.UTC())
		}
	}

	count, err := q.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("weave: count documents: %w", err)
	}
	return count, nil
}

func (s *Store) DeleteDocumentsByCollection(ctx context.Context, colID id.CollectionID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	_, err = s.pg.NewDelete((*documentModel)(nil)).
		Where("collection_id = ?", colID.String()).
		Where("tenant_id = ?", tenant).
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
	if _, err := weave.RequireTenant(ctx); err != nil {
		return err
	}
	for _, ch := range chunks {
		if _, err := tenancy.Owner(ctx, "chunk", ch.TenantID); err != nil {
			return err
		}
	}
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
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	m := new(chunkModel)
	err = s.pg.NewSelect(m).Where("id = ?", chunkID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrChunkNotFound
		}
		return nil, fmt.Errorf("weave: get chunk: %w", err)
	}
	return chunkFromModel(m), nil
}

func (s *Store) ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	var models []chunkModel
	err = s.pg.NewSelect(&models).
		Where("document_id = ?", docID.String()).
		Where("tenant_id = ?", tenant).
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

func (s *Store) ListChunks(ctx context.Context, filter *chunk.ListFilter) ([]*chunk.Chunk, error) {
	tenant, err := tenancy.ChunkList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var models []chunkModel
	q := s.pg.NewSelect(&models).OrderExpr("document_id ASC, index ASC").Where("tenant_id = ?", tenant)
	if filter != nil {
		if filter.DocumentID.String() != "" {
			q = q.Where("document_id = ?", filter.DocumentID.String())
		}
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.Limit > 0 {
			q = q.Limit(filter.Limit)
		}
		if filter.Offset > 0 {
			q = q.Offset(filter.Offset)
		}
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("weave: list chunks: %w", err)
	}
	result := make([]*chunk.Chunk, len(models))
	for i := range models {
		result[i] = chunkFromModel(&models[i])
	}
	return result, nil
}

func (s *Store) DeleteChunksByDocument(ctx context.Context, docID id.DocumentID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	_, err = s.pg.NewDelete((*chunkModel)(nil)).
		Where("document_id = ?", docID.String()).
		Where("tenant_id = ?", tenant).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by document: %w", err)
	}
	return nil
}

func (s *Store) DeleteChunksByCollection(ctx context.Context, colID id.CollectionID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	_, err = s.pg.NewDelete((*chunkModel)(nil)).
		Where("collection_id = ?", colID.String()).
		Where("tenant_id = ?", tenant).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete chunks by collection: %w", err)
	}
	return nil
}

func (s *Store) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	tenant, err := tenancy.ChunkCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	q := s.pg.NewSelect((*chunkModel)(nil)).Where("tenant_id = ?", tenant)

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
	return count, nil
}

// isNoRows checks whether an error indicates no rows were found.
func isNoRows(err error) bool {
	return errors.Is(err, grove.ErrNoRows) || err.Error() == "no rows in result set"
}
