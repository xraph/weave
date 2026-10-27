package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/sqlitedriver"
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

// Store is a SQLite implementation of the composite Weave store
// using Grove ORM with the sqlitedriver backend.
type Store struct {
	db  *grove.DB
	sdb *sqlitedriver.SqliteDB

	// replaceMu serialises ReplaceSourceDocument, TouchDocument and
	// FailDocument within this process, so concurrent claims of one document
	// queue instead of failing with SQLITE_BUSY.
	replaceMu sync.Mutex
}

// New creates a new SQLite store backed by Grove ORM.
func New(db *grove.DB) *Store {
	return &Store{
		db:  db,
		sdb: sqlitedriver.Unwrap(db),
	}
}

// DB returns the underlying grove database for direct access.
func (s *Store) DB() *grove.DB { return s.db }

// Migrate creates the required tables and indexes using the grove orchestrator.
func (s *Store) Migrate(ctx context.Context) error {
	executor, err := migrate.NewExecutorFor(s.sdb)
	if err != nil {
		return fmt.Errorf("weave/sqlite: create migration executor: %w", err)
	}
	orch := migrate.NewOrchestrator(executor, Migrations)
	if _, err := orch.Migrate(ctx); err != nil {
		return fmt.Errorf("%w: %w", weave.ErrMigrationFailed, err)
	}
	return nil
}

// Ping checks database connectivity.
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

	_, err := s.sdb.NewInsert(m).Exec(ctx)
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
	err = s.sdb.NewSelect(m).Where("id = ?", colID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection: %w", err)
	}
	return collectionFromModel(m)
}

func (s *Store) GetCollectionByName(ctx context.Context, tenantID, name string) (*collection.Collection, error) {
	tenant, err := tenancy.Scope(ctx, &tenantID)
	if err != nil {
		return nil, err
	}
	m := new(collectionModel)
	err = s.sdb.NewSelect(m).
		Where("tenant_id = ?", tenant).
		Where("name = ?", name).
		Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection by name: %w", err)
	}
	return collectionFromModel(m)
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

	res, err := s.sdb.NewUpdate(m).WherePK().Where("tenant_id = ?", tenant).Exec(ctx)
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
	res, err := s.sdb.NewDelete((*collectionModel)(nil)).
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
	q := s.sdb.NewSelect(&models).OrderExpr(order).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.Search != "" {
			q = q.Where("name LIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
		}
		q = page(q, filter.Limit, filter.Offset)
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

func (s *Store) CountCollections(ctx context.Context, filter *collection.CountFilter) (int64, error) {
	tenant, err := tenancy.CollectionCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	q := s.sdb.NewSelect((*collectionModel)(nil)).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.Search != "" {
			q = q.Where("name LIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
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
	tenant, err := tenancy.Owner(ctx, "document", doc.TenantID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	m := documentToModel(doc)

	// The collection is checked in the same transaction as the insert, so a
	// document never hangs off another tenant's collection.
	tx, err := s.sdb.BeginTxQuery(ctx, nil)
	if err != nil {
		return fmt.Errorf("weave: create document: %w", err)
	}
	defer rollback(tx)
	n, err := tx.NewSelect((*collectionModel)(nil)).
		Where("id = ?", doc.CollectionID.String()).
		Where("tenant_id = ?", tenant).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("weave: create document: %w", err)
	}
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	if _, err = tx.NewInsert(m).Exec(ctx); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("weave: create document: %w", weave.ErrDuplicateDocument)
		}
		return fmt.Errorf("weave: create document: %w", err)
	}
	if err = tx.Commit(); err != nil {
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
	err = s.sdb.NewSelect(m).Where("id = ?", docID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: get document: %w", err)
	}
	return documentFromModel(m)
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

	res, err := s.sdb.NewUpdate(m).WherePK().
		Where("tenant_id = ?", tenant).
		Where("collection_id = ?", doc.CollectionID.String()).
		Exec(ctx)
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
	res, err := s.sdb.NewDelete((*documentModel)(nil)).
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
	q := s.sdb.NewSelect(&models).OrderExpr(order).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = ?", string(filter.State))
		}
		if filter.SourceKey != "" {
			q = q.Where("source_key = ?", filter.SourceKey)
		}
		if filter.Search != "" {
			q = q.Where("title LIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
		}
		q = page(q, filter.Limit, filter.Offset)
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
	tenant, err := tenancy.DocumentCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	q := s.sdb.NewSelect((*documentModel)(nil)).Where("tenant_id = ?", tenant)

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = ?", string(filter.State))
		}
		if filter.Search != "" {
			q = q.Where("title LIKE '%' || ? || '%' ESCAPE '\\'", sqllike.Escape(filter.Search))
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
	_, err = s.sdb.NewDelete((*documentModel)(nil)).
		Where("collection_id = ?", colID.String()).
		Where("tenant_id = ?", tenant).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete documents by collection: %w", err)
	}
	return nil
}

// TouchDocument records an ingest's heartbeat with one guarded UPDATE, so
// of several claims of one stalled document only the first matches a row.
func (s *Store) TouchDocument(ctx context.Context, docID id.DocumentID, staleBefore time.Time) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	q := s.sdb.NewUpdate((*documentModel)(nil)).Set("state = ?", string(document.StateProcessing))
	return s.transition(ctx, "touch document", tenant, docID, staleBefore, q)
}

// FailDocument marks a pending or processing document failed with one
// guarded UPDATE.
func (s *Store) FailDocument(ctx context.Context, docID id.DocumentID, errText string, staleBefore time.Time) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	q := s.sdb.NewUpdate((*documentModel)(nil)).
		Set("state = ?", string(document.StateFailed)).
		Set("error = ?", errText)
	return s.transition(ctx, "fail document", tenant, docID, staleBefore, q)
}

// transition runs q, an UPDATE that already sets the new state, on docID
// within tenant, but only while the row is pending or processing and, when
// staleBefore is set, last updated before it. When no row matches, it reads
// the row to tell a missing document from one in the wrong state.
func (s *Store) transition(ctx context.Context, op, tenant string, docID id.DocumentID, staleBefore time.Time, q *sqlitedriver.UpdateQuery) error {
	q = q.Set("updated_at = ?", time.Now().UTC()).
		Where("id = ?", docID.String()).
		Where("tenant_id = ?", tenant).
		Where("state IN (?, ?)", string(document.StatePending), string(document.StateProcessing))
	if !staleBefore.IsZero() {
		q = q.Where("updated_at < ?", staleBefore.UTC())
	}
	s.replaceMu.Lock()
	defer s.replaceMu.Unlock()
	res, err := q.Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: %s: %w", op, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("weave: %s rows affected: %w", op, err)
	}
	if n > 0 {
		return nil
	}
	stored := new(documentModel)
	err = s.sdb.NewSelect(stored).Where("id = ?", docID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	switch {
	case err != nil && isNoRows(err):
		return weave.ErrDocumentNotFound
	case err != nil:
		return fmt.Errorf("weave: %s: %w", op, err)
	case stored.State != string(document.StatePending) && stored.State != string(document.StateProcessing):
		return fmt.Errorf("%w: document is %s", weave.ErrInvalidState, stored.State)
	}
	return fmt.Errorf("%w: document was updated since it stalled", weave.ErrInvalidState)
}

// ListStalledDocuments reads across tenants on the (state, updated_at)
// index.
func (s *Store) ListStalledDocuments(ctx context.Context, before time.Time, limit int) ([]*document.Document, error) {
	if limit <= 0 {
		limit = store.DefaultStalledLimit
	}
	var models []documentModel
	err := s.sdb.NewSelect(&models).
		Where("state IN (?, ?, ?)",
			string(document.StatePending), string(document.StateProcessing), string(document.StateSuperseded)).
		Where("updated_at < ?", before.UTC()).
		OrderExpr("updated_at ASC, id ASC").
		Limit(limit).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("weave: list stalled documents: %w", err)
	}
	result := make([]*document.Document, len(models))
	for i := range models {
		d, convErr := documentFromModel(&models[i])
		if convErr != nil {
			return nil, fmt.Errorf("weave: list stalled documents: %w", convErr)
		}
		result[i] = d
	}
	return result, nil
}

// ReplaceSourceDocument runs in one transaction. SQLite lets one writer in
// at a time, and the store's replace mutex keeps this process's replaces
// from opening competing write transactions, so concurrent replaces of one
// source run one after another. Every refusal happens before the first
// write; any later failure rolls the whole replace back.
func (s *Store) ReplaceSourceDocument(ctx context.Context, doc *document.Document, chunks []*chunk.Chunk) ([]id.DocumentID, error) {
	tenant, err := tenancy.SourceReplace(ctx, doc, chunks)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	models := make([]chunkModel, len(chunks))
	for i, ch := range chunks {
		ch.CreatedAt = now
		models[i] = *chunkToModel(ch)
	}
	colID := doc.CollectionID.String()

	s.replaceMu.Lock()
	defer s.replaceMu.Unlock()
	tx, err := s.sdb.BeginTxQuery(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	defer rollback(tx)

	stored := new(documentModel)
	err = tx.NewSelect(stored).Where("id = ?", doc.ID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	err = checkReplaceable(stored, colID, doc.SourceKey)
	if err != nil {
		return nil, err
	}

	var old []documentModel
	err = tx.NewSelect(&old).
		Where("tenant_id = ?", tenant).
		Where("collection_id = ?", colID).
		Where("source_key = ?", doc.SourceKey).
		Where("state IN (?, ?)", string(document.StateReady), string(document.StateFailed)).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	replaced := make([]id.DocumentID, 0, len(old))
	for i := range old {
		oldDoc, convErr := documentFromModel(&old[i])
		if convErr != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", convErr)
		}
		replaced = append(replaced, oldDoc.ID)
		_, err = tx.NewUpdate((*documentModel)(nil)).
			Set("state = ?", string(document.StateSuperseded)).
			Set("updated_at = ?", now).
			Where("id = ?", old[i].ID).
			Where("tenant_id = ?", tenant).
			Exec(ctx)
		if err != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", err)
		}
	}

	// The new chunks go in before the old ones are deleted, so a batch that
	// reuses a superseded chunk's ID fails like any other reused ID.
	if len(models) > 0 {
		if _, err = tx.NewInsert(&models).Exec(ctx); err != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", err)
		}
	}
	for _, docID := range replaced {
		_, err = tx.NewDelete((*chunkModel)(nil)).
			Where("document_id = ?", docID.String()).
			Where("tenant_id = ?", tenant).
			Exec(ctx)
		if err != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", err)
		}
	}

	res, err := tx.NewUpdate((*documentModel)(nil)).
		Set("state = ?", string(document.StateReady)).
		Set("chunk_count = ?", len(chunks)).
		Set("updated_at = ?", now).
		Where("id = ?", doc.ID.String()).
		Where("tenant_id = ?", tenant).
		Where("state = ?", string(document.StateProcessing)).
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("weave: replace source document rows affected: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("%w: document is no longer processing", weave.ErrInvalidState)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	doc.State, doc.ChunkCount, doc.UpdatedAt = document.StateReady, len(chunks), now
	return replaced, nil
}

// checkReplaceable refuses a stored document that ReplaceSourceDocument may
// not finish: one in another collection than the caller named
// (ErrDocumentNotFound, as a document never moves), one whose source key
// differs (ErrInvalidArgument) and one that isn't processing
// (ErrInvalidState).
func checkReplaceable(stored *documentModel, colID, sourceKey string) error {
	if stored.CollectionID != colID {
		return weave.ErrDocumentNotFound
	}
	if stored.SourceKey != sourceKey {
		return fmt.Errorf("%w: source key does not match the stored document's", weave.ErrInvalidArgument)
	}
	if stored.State != string(document.StateProcessing) {
		return fmt.Errorf("%w: document is %s, not processing", weave.ErrInvalidState, stored.State)
	}
	return nil
}

// ──────────────────────────────────────────────────
// Chunk operations
// ──────────────────────────────────────────────────

func (s *Store) CreateChunkBatch(ctx context.Context, chunks []*chunk.Chunk) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	for _, ch := range chunks {
		if _, err = tenancy.Owner(ctx, "chunk", ch.TenantID); err != nil {
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

	// Every chunk's document is checked in the same transaction as the
	// insert: it must belong to the context tenant and sit in the chunk's
	// collection.
	tx, err := s.sdb.BeginTxQuery(ctx, nil)
	if err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	defer rollback(tx)
	err = checkChunkParents(ctx, tx, tenant, chunks)
	if err != nil {
		return err
	}
	if _, err = tx.NewInsert(&models).Exec(ctx); err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	return nil
}

// checkChunkParents reads each distinct document the chunks name, within
// tenant and inside tx. A missing document, or another tenant's, is
// ErrDocumentNotFound; a chunk whose collection is not its document's is
// ErrInvalidArgument.
func checkChunkParents(ctx context.Context, tx *sqlitedriver.SqliteTx, tenant string, chunks []*chunk.Chunk) error {
	collectionOf := make(map[string]string, 1)
	for _, ch := range chunks {
		docID := ch.DocumentID.String()
		colID, ok := collectionOf[docID]
		if !ok {
			m := new(documentModel)
			err := tx.NewSelect(m).Where("id = ?", docID).Where("tenant_id = ?", tenant).Scan(ctx)
			if err != nil {
				if isNoRows(err) {
					return weave.ErrDocumentNotFound
				}
				return fmt.Errorf("weave: create chunk batch: %w", err)
			}
			colID = m.CollectionID
			collectionOf[docID] = colID
		}
		if colID != ch.CollectionID.String() {
			return fmt.Errorf("%w: chunk collection does not match its document's", weave.ErrInvalidArgument)
		}
	}
	return nil
}

// rollback ends tx when it was not committed. After Commit it does nothing.
func rollback(tx interface{ Rollback() error }) {
	_ = tx.Rollback() //nolint:errcheck // nothing to undo after Commit; a failed rollback leaves the server to drop the transaction
}

func (s *Store) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	m := new(chunkModel)
	err = s.sdb.NewSelect(m).Where("id = ?", chunkID.String()).Where("tenant_id = ?", tenant).Scan(ctx)
	if err != nil {
		if isNoRows(err) {
			return nil, weave.ErrChunkNotFound
		}
		return nil, fmt.Errorf("weave: get chunk: %w", err)
	}
	return chunkFromModel(m)
}

func (s *Store) ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	var models []chunkModel
	err = s.sdb.NewSelect(&models).
		Where("document_id = ?", docID.String()).
		Where("tenant_id = ?", tenant).
		OrderExpr(`"index" ASC`).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("weave: list chunks by document: %w", err)
	}

	result := make([]*chunk.Chunk, len(models))
	for i := range models {
		ch, convErr := chunkFromModel(&models[i])
		if convErr != nil {
			return nil, convErr
		}
		result[i] = ch
	}
	return result, nil
}

func (s *Store) ListChunks(ctx context.Context, filter *chunk.ListFilter) ([]*chunk.Chunk, error) {
	tenant, err := tenancy.ChunkList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var models []chunkModel
	q := s.sdb.NewSelect(&models).OrderExpr(`document_id ASC, "index" ASC`).Where("tenant_id = ?", tenant)
	if filter != nil {
		if filter.DocumentID.String() != "" {
			q = q.Where("document_id = ?", filter.DocumentID.String())
		}
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		q = page(q, filter.Limit, filter.Offset)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("weave: list chunks: %w", err)
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
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	_, err = s.sdb.NewDelete((*chunkModel)(nil)).
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
	_, err = s.sdb.NewDelete((*chunkModel)(nil)).
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
	q := s.sdb.NewSelect((*chunkModel)(nil)).Where("tenant_id = ?", tenant)

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

// page applies a limit and offset to a select. SQLite rejects OFFSET without
// LIMIT, and grove drops a non-positive limit instead of passing it through,
// so an offset with no limit gets the largest limit grove can emit, which no
// table reaches.
func page(q *sqlitedriver.SelectQuery, limit, offset int) *sqlitedriver.SelectQuery {
	if limit <= 0 && offset > 0 {
		limit = math.MaxInt32
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	if offset > 0 {
		q = q.Offset(offset)
	}
	return q
}

// isNoRows checks for the standard sql.ErrNoRows sentinel.
func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
