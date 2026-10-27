// Package pgvector provides a PostgreSQL + pgvector implementation of the
// VectorStore interface.
package pgvector

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/xraph/grove"
	"github.com/xraph/grove/driver"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/weave"
	"github.com/xraph/weave/internal/tenancy"
	"github.com/xraph/weave/vectorstore"
)

// Compile-time interface check.
var _ vectorstore.VectorStore = (*Store)(nil)

// VectorEntry is the grove model for a row of the default weave_vectors
// table. The store itself writes raw SQL against whichever table
// WithTableName names; the model is kept for callers that read the table
// through grove.
type VectorEntry struct {
	grove.BaseModel `grove:"table:weave_vectors"`

	ID       string            `grove:"id,pk"`
	Vector   []float32         `grove:"embedding,type:vector"`
	Content  string            `grove:"content"`
	Metadata map[string]string `grove:"metadata,type:jsonb"`
}

// upsertBatch bounds the rows in one INSERT, well under Postgres's 65,535
// bind parameters at four per row.
const upsertBatch = 1000

// Store is a PostgreSQL + pgvector vector store.
type Store struct {
	db         *grove.DB
	pg         *pgdriver.PgDB
	tableName  string
	dimensions int

	// version caches the installed extension's version for Search, read on
	// the first search that succeeds in reading it.
	versionMu sync.Mutex
	version   string

	// skipIterativeScan is set only by tests, to search as a pgvector
	// older than 0.8.0 would.
	skipIterativeScan bool
}

// Option configures the pgvector Store.
type Option func(*Store)

// WithTableName sets a custom table name (default: "weave_vectors"). Every
// statement the store runs names it. Migrations only ever creates
// weave_vectors, so a custom table is yours to create, with the same
// columns.
func WithTableName(name string) Option {
	return func(s *Store) {
		s.tableName = name
	}
}

// WithDimensions sets the embedding size the store expects, the one its
// table was created with. CheckDimensions compares the two.
func WithDimensions(n int) Option {
	return func(s *Store) {
		s.dimensions = n
	}
}

// New creates a new pgvector Store backed by the given grove.DB.
func New(db *grove.DB, opts ...Option) *Store {
	s := &Store{
		db:        db,
		pg:        pgdriver.Unwrap(db),
		tableName: defaultTable,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// CheckDimensions compares the table's embedding column with WithDimensions:
// ErrDimensionMismatch naming both numbers, or ErrNoVectorStore when the
// table does not exist. Without WithDimensions it is ErrInvalidArgument.
func (s *Store) CheckDimensions(ctx context.Context) error {
	if s.dimensions < 1 {
		return fmt.Errorf("%w: pgvector store has no dimensions; set WithDimensions", weave.ErrInvalidArgument)
	}
	return checkTable(ctx, s.pg, s.tableName, s.dimensions)
}

// Upsert inserts or updates vector entries. An entry without
// metadata["tenant_id"] is ErrTenantRequired, and nothing in the batch is
// written. A batch larger than one statement holds is written in one
// transaction.
func (s *Store) Upsert(ctx context.Context, entries []vectorstore.Entry) error {
	if err := tenancy.VectorEntries(entries); err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if len(entries) <= upsertBatch {
		return s.upsert(ctx, s.pg, entries)
	}

	tx, err := s.pg.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("weave: pgvector upsert: %w", err)
	}
	defer rollback(tx)
	for batch := range slices.Chunk(entries, upsertBatch) {
		if err := s.upsert(ctx, tx, batch); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("weave: pgvector upsert: %w", err)
	}
	return nil
}

// execer runs a statement that returns no rows: a pool or a transaction.
type execer interface {
	Exec(ctx context.Context, query string, args ...any) (driver.Result, error)
}

// upsert writes entries with one INSERT ... ON CONFLICT on exec.
func (s *Store) upsert(ctx context.Context, exec execer, entries []vectorstore.Entry) error {
	var b strings.Builder
	b.WriteString("INSERT INTO ")
	b.WriteString(s.table())
	b.WriteString(" (id, embedding, content, metadata) VALUES ")
	args := make([]any, 0, 4*len(entries))
	for i, e := range entries {
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return fmt.Errorf("weave: pgvector upsert %q: encode metadata: %w", e.ID, err)
		}
		if i > 0 {
			b.WriteString(", ")
		}
		n := len(args)
		fmt.Fprintf(&b, "($%d, $%d::vector, $%d, $%d::jsonb)", n+1, n+2, n+3, n+4)
		args = append(args, e.ID, vectorToString(e.Vector), e.Content, string(meta))
	}
	b.WriteString(" ON CONFLICT (id) DO UPDATE SET embedding = EXCLUDED.embedding, content = EXCLUDED.content, metadata = EXCLUDED.metadata")

	if _, err := exec.Exec(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("weave: pgvector upsert: %w", err)
	}
	return nil
}

// Search returns the most similar entries of one tenant to the given
// vector. An empty TenantKey is ErrTenantRequired.
//
// HNSW applies the tenant and metadata filters after it walks the graph, so
// on its own a tenant with few vectors in a large table can get fewer than
// TopK hits, or none. On pgvector 0.8.0 and later Search runs in a
// transaction that first sets hnsw.iterative_scan = strict_order, so the
// index keeps scanning until it has TopK matching rows in exact distance
// order (up to hnsw.max_scan_tuples). Older versions search without it.
func (s *Store) Search(ctx context.Context, vector []float32, opts *vectorstore.SearchOptions) ([]vectorstore.SearchResult, error) {
	if err := tenancy.VectorSearch(opts); err != nil {
		return nil, err
	}
	query, args := s.searchQuery(vector, opts)

	iterative, err := s.iterativeScan(ctx)
	if err != nil {
		return nil, err
	}
	if !iterative {
		return s.search(ctx, s.pg, query, args, opts.MinScore)
	}

	tx, err := s.pg.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("weave: pgvector search: %w", err)
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SET LOCAL hnsw.iterative_scan = strict_order"); err != nil {
		return nil, fmt.Errorf("weave: pgvector search: %w", err)
	}
	results, err := s.search(ctx, tx, query, args, opts.MinScore)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("weave: pgvector search: %w", err)
	}
	return results, nil
}

// searchQuery builds the statement Search runs. opts has passed
// tenancy.VectorSearch, so it is not nil.
func (s *Store) searchQuery(vector []float32, opts *vectorstore.SearchOptions) (query string, args []any) {
	topK := 10
	if opts.TopK > 0 {
		topK = opts.TopK
	}
	args = make([]any, 0, 3+2*len(opts.Filter))
	args = append(args, vectorToString(vector), opts.TenantKey)

	var b strings.Builder
	b.WriteString("SELECT id, content, metadata::text, embedding::text, embedding <=> $1::vector AS distance FROM ")
	b.WriteString(s.table())
	b.WriteString(" WHERE metadata->>'tenant_id' = $2")
	for _, k := range sortedKeys(opts.Filter) {
		fmt.Fprintf(&b, " AND metadata->>$%d = $%d", len(args)+1, len(args)+2)
		args = append(args, k, opts.Filter[k])
	}
	// Break distance ties by ID so the ranking is stable. The HNSW index
	// still orders the scan; an incremental sort only reorders equal
	// distances.
	fmt.Fprintf(&b, " ORDER BY embedding <=> $1::vector, id LIMIT $%d", len(args)+1)
	args = append(args, topK)
	return b.String(), args
}

// search runs query on q and turns the rows into results, dropping any
// below minScore.
func (s *Store) search(ctx context.Context, q queryer, query string, args []any, minScore float64) ([]vectorstore.SearchResult, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("weave: pgvector search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []vectorstore.SearchResult
	for rows.Next() {
		var (
			id, content, meta, embedding string
			distance                     float64
		)
		if err := rows.Scan(&id, &content, &meta, &embedding, &distance); err != nil {
			return nil, fmt.Errorf("weave: pgvector search: %w", err)
		}
		score := 1 - distance // Convert cosine distance to similarity.
		if minScore > 0 && score < minScore {
			continue
		}
		var metadata map[string]string
		if err := json.Unmarshal([]byte(meta), &metadata); err != nil {
			return nil, fmt.Errorf("weave: pgvector search %q: decode metadata: %w", id, err)
		}
		vec, err := parseVector(embedding)
		if err != nil {
			return nil, fmt.Errorf("weave: pgvector search %q: %w", id, err)
		}
		results = append(results, vectorstore.SearchResult{
			Entry: vectorstore.Entry{ID: id, Vector: vec, Content: content, Metadata: metadata},
			Score: score,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("weave: pgvector search: %w", err)
	}
	if results == nil {
		results = []vectorstore.SearchResult{}
	}
	return results, nil
}

// iterativeScan reports whether Search should set hnsw.iterative_scan. It
// reads the extension's version once and keeps it; a failed read is
// returned and tried again on the next search.
func (s *Store) iterativeScan(ctx context.Context) (bool, error) {
	if s.skipIterativeScan {
		return false, nil
	}
	s.versionMu.Lock()
	defer s.versionMu.Unlock()
	if s.version == "" {
		rows, err := s.pg.Query(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`)
		if err != nil {
			return false, fmt.Errorf("weave: pgvector search: read extension version: %w", err)
		}
		version, found, err := firstString(rows)
		if err != nil {
			return false, fmt.Errorf("weave: pgvector search: read extension version: %w", err)
		}
		if !found {
			return false, fmt.Errorf("%w: the vector extension is not installed", weave.ErrNoVectorStore)
		}
		s.version = version
	}
	return supportsIterativeScan(s.version), nil
}

// Delete removes entries by their IDs.
func (s *Store) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := s.pg.Exec(ctx, "DELETE FROM "+s.table()+" WHERE id = ANY($1)", ids); err != nil {
		return fmt.Errorf("weave: pgvector delete: %w", err)
	}
	return nil
}

// DeleteByMetadata removes entries matching the given metadata filter. An
// empty filter would match every row and is ErrInvalidArgument.
func (s *Store) DeleteByMetadata(ctx context.Context, filter map[string]string) error {
	if err := tenancy.VectorDeleteFilter(filter); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("DELETE FROM ")
	b.WriteString(s.table())
	args := make([]any, 0, 2*len(filter))
	for i, k := range sortedKeys(filter) {
		if i == 0 {
			b.WriteString(" WHERE ")
		} else {
			b.WriteString(" AND ")
		}
		fmt.Fprintf(&b, "metadata->>$%d = $%d", len(args)+1, len(args)+2)
		args = append(args, k, filter[k])
	}
	if _, err := s.pg.Exec(ctx, b.String(), args...); err != nil {
		return fmt.Errorf("weave: pgvector delete by metadata: %w", err)
	}
	return nil
}

// rollback ends a transaction a deferred call may already have committed.
func rollback(tx interface{ Rollback() error }) {
	_ = tx.Rollback() //nolint:errcheck // nothing to undo after Commit; a failed rollback leaves the server to drop the transaction
}

// table returns the table name quoted for SQL.
func (s *Store) table() string { return quoteTable(s.tableName) }

// quoteTable quotes name as one SQL identifier.
func quoteTable(name string) string { return pgx.Identifier{name}.Sanitize() }

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// vectorToString converts a float32 slice to pgvector literal format: '[0.1,0.2,0.3]'.
func vectorToString(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// parseVector reads pgvector's text form, "[0.1,0.2,0.3]".
func parseVector(text string) ([]float32, error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(text, "["), "]")
	if inner == "" {
		return []float32{}, nil
	}
	fields := strings.Split(inner, ",")
	v := make([]float32, len(fields))
	for i, f := range fields {
		x, err := strconv.ParseFloat(f, 32)
		if err != nil {
			return nil, fmt.Errorf("parse embedding: %w", err)
		}
		v[i] = float32(x)
	}
	return v, nil
}
