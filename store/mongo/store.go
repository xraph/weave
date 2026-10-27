// Package mongo provides a MongoDB implementation of the Weave
// composite store using Grove ORM with the mongodriver.
package mongo

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/mongodriver"
	"github.com/xraph/grove/drivers/mongodriver/mongomigrate"
	"github.com/xraph/grove/migrate"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/tenancy"
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

// Migrate applies the pending migrations in Migrations (collections and
// indexes) through grove's orchestrator, which records each version in
// grove_migrations. Every Up is idempotent, so an install that ran them
// untracked is adopted in place.
func (s *Store) Migrate(ctx context.Context) error {
	orch := migrate.NewOrchestrator(mongomigrate.New(s.mdb), Migrations)
	if _, err := orch.Migrate(ctx); err != nil {
		return fmt.Errorf("%w: %w", weave.ErrMigrationFailed, err)
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
	if _, err := tenancy.Owner(ctx, "collection", col.TenantID); err != nil {
		return err
	}
	now := time.Now().UTC()
	col.CreatedAt = now
	col.UpdatedAt = now
	m := collectionToModel(col)

	_, err := s.mdb.NewInsert(m).Exec(ctx)
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
	err = s.mdb.NewFind(m).Filter(bson.M{"_id": colID.String(), "tenant_id": tenant}).Scan(ctx)
	if err != nil {
		if isNotFound(err) {
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
	err = s.mdb.NewFind(m).
		Filter(bson.M{"tenant_id": tenant}).
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
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	if col.TenantID != tenant {
		return weave.ErrCollectionNotFound
	}
	col.UpdatedAt = time.Now().UTC()
	m := collectionToModel(col)

	res, err := s.mdb.NewUpdate(m).Filter(bson.M{"_id": m.ID, "tenant_id": tenant}).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("weave: update collection: %w", weave.ErrCollectionAlreadyExists)
		}
		return fmt.Errorf("weave: update collection: %w", err)
	}
	if n := res.MatchedCount(); n == 0 {
		return weave.ErrCollectionNotFound
	}
	return nil
}

func (s *Store) DeleteCollection(ctx context.Context, colID id.CollectionID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	res, err := s.mdb.NewDelete((*collectionModel)(nil)).
		Filter(bson.M{"_id": colID.String(), "tenant_id": tenant}).
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
	tenant, err := tenancy.CollectionList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var models []collectionModel
	dir := 1
	if filter != nil && filter.SortDesc {
		dir = -1
	}
	q := s.mdb.NewFind(&models).
		Filter(bson.M{"tenant_id": tenant}).
		Sort(bson.D{{Key: "created_at", Value: dir}, {Key: "_id", Value: dir}})

	if filter != nil {
		if filter.Search != "" {
			q = q.Filter(bson.M{"name": containsCI(filter.Search)})
		}
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

func (s *Store) CountCollections(ctx context.Context, filter *collection.CountFilter) (int64, error) {
	tenant, err := tenancy.CollectionCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	q := s.mdb.NewFind((*collectionModel)(nil)).Filter(bson.M{"tenant_id": tenant})

	if filter != nil {
		if filter.Search != "" {
			q = q.Filter(bson.M{"name": containsCI(filter.Search)})
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

// CreateDocument persists a new document. Its collection must belong to the
// context tenant, else ErrCollectionNotFound. The check runs before the
// insert, outside a transaction: a collection's tenant never changes, and
// Mongo has no cascade, so a race with a delete can only leave this tenant's
// document without its collection.
func (s *Store) CreateDocument(ctx context.Context, doc *document.Document) error {
	tenant, err := tenancy.Owner(ctx, "document", doc.TenantID)
	if err != nil {
		return err
	}
	n, err := s.mdb.NewFind((*collectionModel)(nil)).
		Filter(bson.M{"_id": doc.CollectionID.String(), "tenant_id": tenant}).
		Count(ctx)
	if err != nil {
		return fmt.Errorf("weave: create document: %w", err)
	}
	if n == 0 {
		return weave.ErrCollectionNotFound
	}
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	m := documentToModel(doc)

	_, err = s.mdb.NewInsert(m).Exec(ctx)
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
	err = s.mdb.NewFind(m).Filter(bson.M{"_id": docID.String(), "tenant_id": tenant}).Scan(ctx)
	if err != nil {
		if isNotFound(err) {
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

	res, err := s.mdb.NewUpdate(m).
		Filter(bson.M{"_id": m.ID, "tenant_id": tenant, "collection_id": m.CollectionID}).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: update document: %w", err)
	}
	if n := res.MatchedCount(); n == 0 {
		return weave.ErrDocumentNotFound
	}
	return nil
}

func (s *Store) DeleteDocument(ctx context.Context, docID id.DocumentID) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	res, err := s.mdb.NewDelete((*documentModel)(nil)).
		Filter(bson.M{"_id": docID.String(), "tenant_id": tenant}).
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
	tenant, err := tenancy.DocumentList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var models []documentModel
	dir := 1
	if filter != nil && filter.SortDesc {
		dir = -1
	}
	q := s.mdb.NewFind(&models).
		Filter(bson.M{"tenant_id": tenant}).
		Sort(bson.D{{Key: "created_at", Value: dir}, {Key: "_id", Value: dir}})

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Filter(bson.M{"collection_id": filter.CollectionID.String()})
		}
		if filter.State != "" {
			q = q.Filter(bson.M{"state": string(filter.State)})
		}
		if filter.SourceKey != "" {
			q = q.Filter(bson.M{"source_key": filter.SourceKey})
		}
		if filter.Search != "" {
			q = q.Filter(bson.M{"title": containsCI(filter.Search)})
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
	tenant, err := tenancy.DocumentCount(ctx, filter)
	if err != nil {
		return 0, err
	}
	q := s.mdb.NewFind((*documentModel)(nil)).Filter(bson.M{"tenant_id": tenant})

	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Filter(bson.M{"collection_id": filter.CollectionID.String()})
		}
		if filter.State != "" {
			q = q.Filter(bson.M{"state": string(filter.State)})
		}
		if filter.Search != "" {
			q = q.Filter(bson.M{"title": containsCI(filter.Search)})
		}
		if !filter.UpdatedBefore.IsZero() {
			q = q.Filter(bson.M{"updated_at": bson.M{"$lt": filter.UpdatedBefore.UTC()}})
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
	_, err = s.mdb.NewDelete((*documentModel)(nil)).
		Filter(bson.M{"collection_id": colID.String(), "tenant_id": tenant}).
		Many().
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: delete documents by collection: %w", err)
	}
	return nil
}

// TouchDocument records an ingest's heartbeat with one filtered UpdateOne,
// so of several claims of one stalled document only the first matches.
func (s *Store) TouchDocument(ctx context.Context, docID id.DocumentID, staleBefore time.Time) error {
	return s.transition(ctx, "touch document", docID, staleBefore, bson.M{"state": string(document.StateProcessing)})
}

// FailDocument marks a pending or processing document failed with one
// filtered UpdateOne.
func (s *Store) FailDocument(ctx context.Context, docID id.DocumentID, errText string, staleBefore time.Time) error {
	return s.transition(ctx, "fail document", docID, staleBefore, bson.M{"state": string(document.StateFailed), "error": errText})
}

// transition sets fields, and updated_at, on docID within the context
// tenant, but only while the document is pending or processing and, when
// staleBefore is set, last updated before it. When nothing matches, it reads
// the document to tell a missing one from one in the wrong state.
func (s *Store) transition(ctx context.Context, op string, docID id.DocumentID, staleBefore time.Time, fields bson.M) error {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return err
	}
	filter := bson.M{
		"_id":       docID.String(),
		"tenant_id": tenant,
		"state":     bson.M{"$in": bson.A{string(document.StatePending), string(document.StateProcessing)}},
	}
	if !staleBefore.IsZero() {
		filter["updated_at"] = bson.M{"$lt": staleBefore.UTC()}
	}
	set := bson.M{"updated_at": time.Now().UTC()}
	maps.Copy(set, fields)
	res, err := s.mdb.NewUpdate((*documentModel)(nil)).Filter(filter).SetUpdate(bson.M{"$set": set}).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: %s: %w", op, err)
	}
	if res.MatchedCount() > 0 {
		return nil
	}
	stored := new(documentModel)
	err = s.mdb.NewFind(stored).Filter(bson.M{"_id": docID.String(), "tenant_id": tenant}).Scan(ctx)
	switch {
	case err != nil && isNotFound(err):
		return weave.ErrDocumentNotFound
	case err != nil:
		return fmt.Errorf("weave: %s: %w", op, err)
	case stored.State != string(document.StatePending) && stored.State != string(document.StateProcessing):
		return fmt.Errorf("%w: document is %s", weave.ErrInvalidState, stored.State)
	}
	return fmt.Errorf("%w: document was updated since it stalled", weave.ErrInvalidState)
}

// ListStalledDocuments reads across tenants on the {state, updated_at}
// index.
func (s *Store) ListStalledDocuments(ctx context.Context, before time.Time, limit int) ([]*document.Document, error) {
	if limit <= 0 {
		limit = store.DefaultStalledLimit
	}
	var models []documentModel
	err := s.mdb.NewFind(&models).
		Filter(bson.M{
			"state": bson.M{"$in": bson.A{
				string(document.StatePending), string(document.StateProcessing), string(document.StateSuperseded),
			}},
			"updated_at": bson.M{"$lt": before.UTC()},
		}).
		Sort(bson.D{{Key: "updated_at", Value: 1}, {Key: "_id", Value: 1}}).
		Limit(int64(limit)).
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

// ReplaceSourceDocument runs in a multi-document transaction, so it needs a
// replica set (or mongos); on a standalone server it fails with an error
// that says so. The session's WithTransaction reruns the whole step when the
// server reports a transient conflict, which is how two replaces of one
// source that collide end up one after the other. The document read and the
// chunk ID check happen inside the transaction, and every refusal comes
// before the first write.
func (s *Store) ReplaceSourceDocument(ctx context.Context, doc *document.Document, chunks []*chunk.Chunk) ([]id.DocumentID, error) {
	tenant, err := tenancy.SourceReplace(ctx, doc, chunks)
	if err != nil {
		return nil, err
	}
	sess, err := s.mdb.Client().StartSession()
	if err != nil {
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	defer sess.EndSession(ctx)

	var now time.Time
	out, err := sess.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		now = time.Now().UTC()
		return s.replaceSource(txCtx, tenant, doc, chunks, now)
	})
	if err != nil {
		if cerr, ok := transactionsUnsupported(err); ok {
			return nil, fmt.Errorf("weave: replace source document needs a MongoDB replica set: %w", cerr)
		}
		return nil, err
	}
	replaced, ok := out.([]id.DocumentID)
	if !ok {
		return nil, fmt.Errorf("weave: replace source document: unexpected result %T", out)
	}
	doc.State, doc.ChunkCount, doc.UpdatedAt = document.StateReady, len(chunks), now
	return replaced, nil
}

// replaceSource is one attempt at ReplaceSourceDocument. txCtx carries the
// session, so every query here joins its transaction.
func (s *Store) replaceSource(txCtx context.Context, tenant string, doc *document.Document, chunks []*chunk.Chunk, now time.Time) ([]id.DocumentID, error) {
	stored := new(documentModel)
	err := s.mdb.NewFind(stored).Filter(bson.M{"_id": doc.ID.String(), "tenant_id": tenant}).Scan(txCtx)
	if err != nil {
		if isNotFound(err) {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	colID := doc.CollectionID.String()
	switch {
	case stored.CollectionID != colID:
		return nil, weave.ErrDocumentNotFound
	case stored.SourceKey != doc.SourceKey:
		return nil, fmt.Errorf("%w: source key does not match the stored document's", weave.ErrInvalidArgument)
	case stored.State != string(document.StateProcessing):
		return nil, fmt.Errorf("%w: document is %s, not processing", weave.ErrInvalidState, stored.State)
	}
	if len(chunks) > 0 {
		err = s.checkChunkIDs(txCtx, chunks)
		if err != nil {
			return nil, err
		}
	}

	var old []documentModel
	sourceFilter := bson.M{
		"tenant_id":     tenant,
		"collection_id": colID,
		"source_key":    doc.SourceKey,
		"state":         bson.M{"$in": bson.A{string(document.StateReady), string(document.StateFailed)}},
	}
	if err = s.mdb.NewFind(&old).Filter(sourceFilter).Scan(txCtx); err != nil {
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	replaced := make([]id.DocumentID, 0, len(old))
	oldIDs := make(bson.A, 0, len(old))
	for i := range old {
		oldDoc, convErr := documentFromModel(&old[i])
		if convErr != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", convErr)
		}
		replaced = append(replaced, oldDoc.ID)
		oldIDs = append(oldIDs, old[i].ID)
	}
	if len(oldIDs) > 0 {
		_, err = s.mdb.NewUpdate((*documentModel)(nil)).
			Filter(bson.M{"_id": bson.M{"$in": oldIDs}, "tenant_id": tenant}).
			SetUpdate(bson.M{"$set": bson.M{"state": string(document.StateSuperseded), "updated_at": now}}).
			Many().
			Exec(txCtx)
		if err != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", err)
		}
		_, err = s.mdb.NewDelete((*chunkModel)(nil)).
			Filter(bson.M{"document_id": bson.M{"$in": oldIDs}, "tenant_id": tenant}).
			Many().
			Exec(txCtx)
		if err != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", err)
		}
	}

	if len(chunks) > 0 {
		models := make([]chunkModel, len(chunks))
		for i, ch := range chunks {
			ch.CreatedAt = now
			models[i] = *chunkToModel(ch)
		}
		if _, err = s.mdb.NewInsert(&models).Exec(txCtx); err != nil {
			return nil, fmt.Errorf("weave: replace source document: %w", err)
		}
	}

	res, err := s.mdb.NewUpdate((*documentModel)(nil)).
		Filter(bson.M{"_id": doc.ID.String(), "tenant_id": tenant, "state": string(document.StateProcessing)}).
		SetUpdate(bson.M{"$set": bson.M{"state": string(document.StateReady), "chunk_count": len(chunks), "updated_at": now}}).
		Exec(txCtx)
	if err != nil {
		return nil, fmt.Errorf("weave: replace source document: %w", err)
	}
	if res.MatchedCount() == 0 {
		return nil, fmt.Errorf("%w: document is no longer processing", weave.ErrInvalidState)
	}
	return replaced, nil
}

// ──────────────────────────────────────────────────
// Chunk operations
// ──────────────────────────────────────────────────

// CreateChunkBatch persists a batch of chunks. Each chunk's document must
// belong to the context tenant and sit in the chunk's collection; that is
// checked for the whole batch before the insert, as CreateDocument checks
// its collection.
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
	err = s.checkChunkParents(ctx, tenant, chunks)
	if err != nil {
		return err
	}
	err = s.checkChunkIDs(ctx, chunks)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	models := make([]chunkModel, len(chunks))
	for i, ch := range chunks {
		ch.CreatedAt = now
		models[i] = *chunkToModel(ch)
	}

	_, err = s.mdb.NewInsert(&models).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	return nil
}

// checkChunkIDs refuses a batch that repeats a chunk ID or reuses one already
// stored, under any tenant, so a batch never half-lands on a duplicate key.
// CreateChunkBatch runs it without a transaction, so that it keeps working
// on a standalone server, and a concurrent insert of the same ID can still
// slip between this check and the insert there. ReplaceSourceDocument runs
// it inside its transaction, where it can't.
func (s *Store) checkChunkIDs(ctx context.Context, chunks []*chunk.Chunk) error {
	ids := make([]string, 0, len(chunks))
	seen := make(map[string]bool, len(chunks))
	for _, ch := range chunks {
		key := ch.ID.String()
		if seen[key] {
			return fmt.Errorf("weave: create chunk batch: chunk %s appears twice", key)
		}
		seen[key] = true
		ids = append(ids, key)
	}
	n, err := s.mdb.NewFind((*chunkModel)(nil)).Filter(bson.M{"_id": bson.M{"$in": ids}}).Count(ctx)
	if err != nil {
		return fmt.Errorf("weave: create chunk batch: %w", err)
	}
	if n > 0 {
		return errors.New("weave: create chunk batch: a chunk with one of these IDs already exists")
	}
	return nil
}

// checkChunkParents reads each distinct document the chunks name, within
// tenant. A missing document, or another tenant's, is ErrDocumentNotFound; a
// chunk whose collection is not its document's is ErrInvalidArgument.
func (s *Store) checkChunkParents(ctx context.Context, tenant string, chunks []*chunk.Chunk) error {
	collectionOf := make(map[string]string, 1)
	for _, ch := range chunks {
		docID := ch.DocumentID.String()
		colID, ok := collectionOf[docID]
		if !ok {
			m := new(documentModel)
			err := s.mdb.NewFind(m).Filter(bson.M{"_id": docID, "tenant_id": tenant}).Scan(ctx)
			if err != nil {
				if isNotFound(err) {
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

func (s *Store) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return nil, err
	}
	m := new(chunkModel)
	err = s.mdb.NewFind(m).Filter(bson.M{"_id": chunkID.String(), "tenant_id": tenant}).Scan(ctx)
	if err != nil {
		if isNotFound(err) {
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
	err = s.mdb.NewFind(&models).
		Filter(bson.M{"document_id": docID.String(), "tenant_id": tenant}).
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

func (s *Store) ListChunks(ctx context.Context, filter *chunk.ListFilter) ([]*chunk.Chunk, error) {
	tenant, err := tenancy.ChunkList(ctx, filter)
	if err != nil {
		return nil, err
	}
	var models []chunkModel
	q := s.mdb.NewFind(&models).
		Filter(bson.M{"tenant_id": tenant}).
		Sort(bson.D{{Key: "document_id", Value: 1}, {Key: "index", Value: 1}})
	if filter != nil {
		if filter.DocumentID.String() != "" {
			q = q.Filter(bson.M{"document_id": filter.DocumentID.String()})
		}
		if filter.CollectionID.String() != "" {
			q = q.Filter(bson.M{"collection_id": filter.CollectionID.String()})
		}
		if filter.Limit > 0 {
			q = q.Limit(int64(filter.Limit))
		}
		if filter.Offset > 0 {
			q = q.Skip(int64(filter.Offset))
		}
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
	_, err = s.mdb.NewDelete((*chunkModel)(nil)).
		Filter(bson.M{"document_id": docID.String(), "tenant_id": tenant}).
		Many().
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
	_, err = s.mdb.NewDelete((*chunkModel)(nil)).
		Filter(bson.M{"collection_id": colID.String(), "tenant_id": tenant}).
		Many().
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
	q := s.mdb.NewFind((*chunkModel)(nil)).Filter(bson.M{"tenant_id": tenant})

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

// containsCI matches s anywhere in the field, case-insensitively, treating
// every character literally. User input must never reach $regex unquoted:
// "(" is a regex error and ".*" matches everything.
func containsCI(s string) bson.M {
	return bson.M{"$regex": regexp.QuoteMeta(s), "$options": "i"}
}
