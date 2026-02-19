// Package sqlite provides a SQLite implementation of the Weave composite
// store using bun ORM. Suitable for single-node deployments and local development.
package sqlite

import (
	"context"
	"database/sql"
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

// Compile-time interface check.
var _ store.Store = (*Store)(nil)

// Store is a SQLite implementation of the composite Weave store.
type Store struct {
	db *bun.DB
}

// New creates a new SQLite store.
func New(db *bun.DB) *Store {
	return &Store{db: db}
}

// Migrate creates tables if they don't exist (SQLite-compatible DDL).
func (s *Store) Migrate(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS weave_collections (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			description TEXT,
			tenant_id TEXT NOT NULL,
			app_id TEXT NOT NULL,
			embedding_model TEXT NOT NULL DEFAULT 'text-embedding-3-small',
			embedding_dims INTEGER NOT NULL DEFAULT 1536,
			chunk_strategy TEXT NOT NULL DEFAULT 'recursive',
			chunk_size INTEGER NOT NULL DEFAULT 512,
			chunk_overlap INTEGER NOT NULL DEFAULT 50,
			metadata TEXT NOT NULL DEFAULT '{}',
			document_count INTEGER NOT NULL DEFAULT 0,
			chunk_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(tenant_id, name)
		)`,
		`CREATE TABLE IF NOT EXISTS weave_documents (
			id TEXT PRIMARY KEY,
			collection_id TEXT NOT NULL REFERENCES weave_collections(id) ON DELETE CASCADE,
			tenant_id TEXT NOT NULL,
			title TEXT,
			source TEXT,
			source_type TEXT,
			content_hash TEXT NOT NULL,
			content_length INTEGER NOT NULL DEFAULT 0,
			chunk_count INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT '{}',
			state TEXT NOT NULL DEFAULT 'pending',
			error TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			UNIQUE(collection_id, content_hash)
		)`,
		`CREATE TABLE IF NOT EXISTS weave_chunks (
			id TEXT PRIMARY KEY,
			document_id TEXT NOT NULL REFERENCES weave_documents(id) ON DELETE CASCADE,
			collection_id TEXT NOT NULL REFERENCES weave_collections(id) ON DELETE CASCADE,
			tenant_id TEXT NOT NULL,
			content TEXT NOT NULL,
			"index" INTEGER NOT NULL,
			start_offset INTEGER NOT NULL DEFAULT 0,
			end_offset INTEGER NOT NULL DEFAULT 0,
			token_count INTEGER NOT NULL DEFAULT 0,
			metadata TEXT NOT NULL DEFAULT '{}',
			parent_id TEXT,
			created_at TEXT NOT NULL
		)`,
	}

	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("weave: %w: %w", weave.ErrMigrationFailed, err)
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
// Collection operations (reuse postgres model types inline)
// ──────────────────────────────────────────────────

type collectionRow struct {
	bun.BaseModel `bun:"table:weave_collections"`

	ID             string `bun:"id,pk"`
	Name           string `bun:"name,notnull"`
	Description    string `bun:"description"`
	TenantID       string `bun:"tenant_id,notnull"`
	AppID          string `bun:"app_id,notnull"`
	EmbeddingModel string `bun:"embedding_model,notnull"`
	EmbeddingDims  int    `bun:"embedding_dims,notnull"`
	ChunkStrategy  string `bun:"chunk_strategy,notnull"`
	ChunkSize      int    `bun:"chunk_size,notnull"`
	ChunkOverlap   int    `bun:"chunk_overlap,notnull"`
	Metadata       string `bun:"metadata"` // JSON string for SQLite.
	DocumentCount  int64  `bun:"document_count,notnull"`
	ChunkCount     int64  `bun:"chunk_count,notnull"`
	CreatedAt      string `bun:"created_at,notnull"`
	UpdatedAt      string `bun:"updated_at,notnull"`
}

func (s *Store) CreateCollection(ctx context.Context, col *collection.Collection) error {
	now := time.Now().UTC()
	col.CreatedAt = now
	col.UpdatedAt = now

	row := &collectionRow{
		ID: col.ID.String(), Name: col.Name, Description: col.Description,
		TenantID: col.TenantID, AppID: col.AppID,
		EmbeddingModel: col.EmbeddingModel, EmbeddingDims: col.EmbeddingDims,
		ChunkStrategy: col.ChunkStrategy, ChunkSize: col.ChunkSize, ChunkOverlap: col.ChunkOverlap,
		Metadata: "{}", DocumentCount: col.DocumentCount, ChunkCount: col.ChunkCount,
		CreatedAt: now.Format(time.RFC3339Nano), UpdatedAt: now.Format(time.RFC3339Nano),
	}
	_, err := s.db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create collection: %w", err)
	}
	return nil
}

func (s *Store) GetCollection(ctx context.Context, colID id.CollectionID) (*collection.Collection, error) {
	row := new(collectionRow)
	err := s.db.NewSelect().Model(row).Where("id = ?", colID.String()).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection: %w", err)
	}
	return rowToCollection(row), nil
}

func (s *Store) GetCollectionByName(ctx context.Context, tenantID, name string) (*collection.Collection, error) {
	row := new(collectionRow)
	err := s.db.NewSelect().Model(row).
		Where("tenant_id = ?", tenantID).Where("name = ?", name).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrCollectionNotFound
		}
		return nil, fmt.Errorf("weave: get collection by name: %w", err)
	}
	return rowToCollection(row), nil
}

func (s *Store) UpdateCollection(ctx context.Context, col *collection.Collection) error {
	col.UpdatedAt = time.Now().UTC()
	row := &collectionRow{
		ID: col.ID.String(), Name: col.Name, Description: col.Description,
		TenantID: col.TenantID, AppID: col.AppID,
		EmbeddingModel: col.EmbeddingModel, EmbeddingDims: col.EmbeddingDims,
		ChunkStrategy: col.ChunkStrategy, ChunkSize: col.ChunkSize, ChunkOverlap: col.ChunkOverlap,
		Metadata: "{}", DocumentCount: col.DocumentCount, ChunkCount: col.ChunkCount,
		CreatedAt: col.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: col.UpdatedAt.Format(time.RFC3339Nano),
	}
	res, err := s.db.NewUpdate().Model(row).WherePK().Exec(ctx)
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
	res, err := s.db.NewDelete().Model((*collectionRow)(nil)).Where("id = ?", colID.String()).Exec(ctx)
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
	var rows []collectionRow
	q := s.db.NewSelect().Model(&rows).OrderExpr("created_at ASC")
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
	result := make([]*collection.Collection, len(rows))
	for i := range rows {
		result[i] = rowToCollection(&rows[i])
	}
	return result, nil
}

// ──────────────────────────────────────────────────
// Document operations
// ──────────────────────────────────────────────────

type documentRow struct {
	bun.BaseModel `bun:"table:weave_documents"`

	ID            string `bun:"id,pk"`
	CollectionID  string `bun:"collection_id,notnull"`
	TenantID      string `bun:"tenant_id,notnull"`
	Title         string `bun:"title"`
	Source        string `bun:"source"`
	SourceType    string `bun:"source_type"`
	ContentHash   string `bun:"content_hash,notnull"`
	ContentLength int    `bun:"content_length,notnull"`
	ChunkCount    int    `bun:"chunk_count,notnull"`
	Metadata      string `bun:"metadata"`
	State         string `bun:"state,notnull"`
	Error         string `bun:"error"`
	CreatedAt     string `bun:"created_at,notnull"`
	UpdatedAt     string `bun:"updated_at,notnull"`
}

func (s *Store) CreateDocument(ctx context.Context, doc *document.Document) error {
	now := time.Now().UTC()
	doc.CreatedAt = now
	doc.UpdatedAt = now
	row := docToRow(doc, now)
	_, err := s.db.NewInsert().Model(row).Exec(ctx)
	if err != nil {
		return fmt.Errorf("weave: create document: %w", err)
	}
	return nil
}

func (s *Store) GetDocument(ctx context.Context, docID id.DocumentID) (*document.Document, error) {
	row := new(documentRow)
	err := s.db.NewSelect().Model(row).Where("id = ?", docID.String()).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrDocumentNotFound
		}
		return nil, fmt.Errorf("weave: get document: %w", err)
	}
	return rowToDocument(row), nil
}

func (s *Store) UpdateDocument(ctx context.Context, doc *document.Document) error {
	doc.UpdatedAt = time.Now().UTC()
	row := docToRow(doc, doc.UpdatedAt)
	res, err := s.db.NewUpdate().Model(row).WherePK().Exec(ctx)
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
	res, err := s.db.NewDelete().Model((*documentRow)(nil)).Where("id = ?", docID.String()).Exec(ctx)
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
	var rows []documentRow
	q := s.db.NewSelect().Model(&rows).OrderExpr("created_at ASC")
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
	result := make([]*document.Document, len(rows))
	for i := range rows {
		result[i] = rowToDocument(&rows[i])
	}
	return result, nil
}

func (s *Store) CountDocuments(ctx context.Context, filter *document.CountFilter) (int64, error) {
	q := s.db.NewSelect().Model((*documentRow)(nil))
	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.State != "" {
			q = q.Where("state = ?", string(filter.State))
		}
	}
	count, err := q.Count(ctx)
	return int64(count), err
}

func (s *Store) DeleteDocumentsByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.db.NewDelete().Model((*documentRow)(nil)).Where("collection_id = ?", colID.String()).Exec(ctx)
	return err
}

// ──────────────────────────────────────────────────
// Chunk operations
// ──────────────────────────────────────────────────

type chunkRow struct {
	bun.BaseModel `bun:"table:weave_chunks"`

	ID           string `bun:"id,pk"`
	DocumentID   string `bun:"document_id,notnull"`
	CollectionID string `bun:"collection_id,notnull"`
	TenantID     string `bun:"tenant_id,notnull"`
	Content      string `bun:"content,notnull"`
	Index        int    `bun:"index,notnull"`
	StartOffset  int    `bun:"start_offset,notnull"`
	EndOffset    int    `bun:"end_offset,notnull"`
	TokenCount   int    `bun:"token_count,notnull"`
	Metadata     string `bun:"metadata"`
	ParentID     string `bun:"parent_id"`
	CreatedAt    string `bun:"created_at,notnull"`
}

func (s *Store) CreateChunkBatch(ctx context.Context, chunks []*chunk.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	now := time.Now().UTC()
	rows := make([]chunkRow, len(chunks))
	for i, ch := range chunks {
		ch.CreatedAt = now
		rows[i] = chunkRow{
			ID: ch.ID.String(), DocumentID: ch.DocumentID.String(),
			CollectionID: ch.CollectionID.String(), TenantID: ch.TenantID,
			Content: ch.Content, Index: ch.Index,
			StartOffset: ch.StartOffset, EndOffset: ch.EndOffset,
			TokenCount: ch.TokenCount, Metadata: "{}",
			ParentID: ch.ParentID, CreatedAt: now.Format(time.RFC3339Nano),
		}
	}
	_, err := s.db.NewInsert().Model(&rows).Exec(ctx)
	return err
}

func (s *Store) GetChunk(ctx context.Context, chunkID id.ChunkID) (*chunk.Chunk, error) {
	row := new(chunkRow)
	err := s.db.NewSelect().Model(row).Where("id = ?", chunkID.String()).Scan(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, weave.ErrChunkNotFound
		}
		return nil, fmt.Errorf("weave: get chunk: %w", err)
	}
	return rowToChunk(row), nil
}

func (s *Store) ListChunksByDocument(ctx context.Context, docID id.DocumentID) ([]*chunk.Chunk, error) {
	var rows []chunkRow
	err := s.db.NewSelect().Model(&rows).Where("document_id = ?", docID.String()).
		OrderExpr(`"index" ASC`).Scan(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*chunk.Chunk, len(rows))
	for i := range rows {
		result[i] = rowToChunk(&rows[i])
	}
	return result, nil
}

func (s *Store) DeleteChunksByDocument(ctx context.Context, docID id.DocumentID) error {
	_, err := s.db.NewDelete().Model((*chunkRow)(nil)).Where("document_id = ?", docID.String()).Exec(ctx)
	return err
}

func (s *Store) DeleteChunksByCollection(ctx context.Context, colID id.CollectionID) error {
	_, err := s.db.NewDelete().Model((*chunkRow)(nil)).Where("collection_id = ?", colID.String()).Exec(ctx)
	return err
}

func (s *Store) CountChunks(ctx context.Context, filter *chunk.CountFilter) (int64, error) {
	q := s.db.NewSelect().Model((*chunkRow)(nil))
	if filter != nil {
		if filter.CollectionID.String() != "" {
			q = q.Where("collection_id = ?", filter.CollectionID.String())
		}
		if filter.DocumentID.String() != "" {
			q = q.Where("document_id = ?", filter.DocumentID.String())
		}
	}
	count, err := q.Count(ctx)
	return int64(count), err
}

// ──────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────

func rowToCollection(r *collectionRow) *collection.Collection {
	colID, _ := id.ParseCollectionID(r.ID)
	ca, _ := time.Parse(time.RFC3339Nano, r.CreatedAt)
	ua, _ := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	c := &collection.Collection{
		ID: colID, Name: r.Name, Description: r.Description,
		TenantID: r.TenantID, AppID: r.AppID,
		EmbeddingModel: r.EmbeddingModel, EmbeddingDims: r.EmbeddingDims,
		ChunkStrategy: r.ChunkStrategy, ChunkSize: r.ChunkSize, ChunkOverlap: r.ChunkOverlap,
		DocumentCount: r.DocumentCount, ChunkCount: r.ChunkCount,
	}
	c.CreatedAt = ca
	c.UpdatedAt = ua
	return c
}

func rowToDocument(r *documentRow) *document.Document {
	docID, _ := id.ParseDocumentID(r.ID)
	colID, _ := id.ParseCollectionID(r.CollectionID)
	d := &document.Document{
		ID: docID, CollectionID: colID, TenantID: r.TenantID,
		Title: r.Title, Source: r.Source, SourceType: r.SourceType,
		ContentHash: r.ContentHash, ContentLength: r.ContentLength,
		ChunkCount: r.ChunkCount, State: document.State(r.State), Error: r.Error,
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, r.CreatedAt)
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, r.UpdatedAt)
	return d
}

func rowToChunk(r *chunkRow) *chunk.Chunk {
	chkID, _ := id.ParseChunkID(r.ID)
	docID, _ := id.ParseDocumentID(r.DocumentID)
	colID, _ := id.ParseCollectionID(r.CollectionID)
	c := &chunk.Chunk{
		ID: chkID, DocumentID: docID, CollectionID: colID,
		TenantID: r.TenantID, Content: r.Content, Index: r.Index,
		StartOffset: r.StartOffset, EndOffset: r.EndOffset,
		TokenCount: r.TokenCount, ParentID: r.ParentID,
	}
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, r.CreatedAt)
	return c
}

func docToRow(d *document.Document, now time.Time) *documentRow {
	return &documentRow{
		ID: d.ID.String(), CollectionID: d.CollectionID.String(),
		TenantID: d.TenantID, Title: d.Title, Source: d.Source,
		SourceType: d.SourceType, ContentHash: d.ContentHash,
		ContentLength: d.ContentLength, ChunkCount: d.ChunkCount,
		Metadata: "{}", State: string(d.State), Error: d.Error,
		CreatedAt: d.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt: now.Format(time.RFC3339Nano),
	}
}
