// Package pgvector provides a PostgreSQL + pgvector implementation of the
// VectorStore interface.
package pgvector

import (
	"context"
	"fmt"
	"strings"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/weave/vectorstore"
)

// Compile-time interface check.
var _ vectorstore.VectorStore = (*Store)(nil)

// VectorEntry is the grove model for vector entries stored in PostgreSQL.
type VectorEntry struct {
	grove.BaseModel `grove:"table:weave_vectors"`

	ID       string            `grove:"id,pk"`
	Vector   []float32         `grove:"embedding,type:vector"`
	Content  string            `grove:"content"`
	Metadata map[string]string `grove:"metadata,type:jsonb"`
}

// vectorSearchRow is used to scan search results that include a computed distance column.
type vectorSearchRow struct {
	grove.BaseModel `grove:"table:weave_vectors"`

	ID       string            `grove:"id"`
	Content  string            `grove:"content"`
	Metadata map[string]string `grove:"metadata,type:jsonb"`
	Vector   []float32         `grove:"embedding,type:vector"`
	Distance float64           `grove:"distance"`
}

// Store is a PostgreSQL + pgvector vector store.
type Store struct {
	db        *grove.DB
	pg        *pgdriver.PgDB
	tableName string
}

// Option configures the pgvector Store.
type Option func(*Store)

// WithTableName sets a custom table name (default: "weave_vectors").
func WithTableName(name string) Option {
	return func(s *Store) {
		s.tableName = name
	}
}

// New creates a new pgvector Store backed by the given grove.DB.
func New(db *grove.DB, opts ...Option) *Store {
	s := &Store{
		db:        db,
		pg:        pgdriver.Unwrap(db),
		tableName: "weave_vectors",
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Upsert inserts or updates vector entries.
func (s *Store) Upsert(ctx context.Context, entries []vectorstore.Entry) error {
	models := make([]VectorEntry, len(entries))
	for i, e := range entries {
		models[i] = VectorEntry{
			ID:       e.ID,
			Vector:   e.Vector,
			Content:  e.Content,
			Metadata: e.Metadata,
		}
	}

	_, err := s.pg.NewInsert(&models).
		OnConflict("(id) DO UPDATE").
		Set("embedding = EXCLUDED.embedding").
		Set("content = EXCLUDED.content").
		Set("metadata = EXCLUDED.metadata").
		Exec(ctx)
	return err
}

// Search returns the most similar entries to the given vector.
func (s *Store) Search(ctx context.Context, vector []float32, opts *vectorstore.SearchOptions) ([]vectorstore.SearchResult, error) {
	topK := 10
	if opts != nil && opts.TopK > 0 {
		topK = opts.TopK
	}

	vecStr := vectorToString(vector)

	// Inline the vector literal in SQL expressions. This is safe because
	// vecStr is generated from float32 values, not user input.
	colExpr := fmt.Sprintf(
		"id, content, metadata, embedding, (embedding <=> '%s'::vector) AS distance",
		vecStr,
	)
	orderExpr := fmt.Sprintf("embedding <=> '%s'::vector", vecStr)

	q := s.pg.NewSelect().
		TableExpr(s.tableName).
		ColumnExpr(colExpr).
		OrderExpr(orderExpr).
		Limit(topK)

	// Track parameter index for Where clauses (no args from ColumnExpr/TableExpr).
	paramIdx := 1
	if opts != nil && opts.TenantKey != "" {
		q = q.Where(fmt.Sprintf("metadata->>'tenant_id' = $%d", paramIdx), opts.TenantKey)
		paramIdx++
	}

	if opts != nil {
		for k, v := range opts.Filter {
			q = q.Where(fmt.Sprintf("metadata->>$%d = $%d", paramIdx, paramIdx+1), k, v)
			paramIdx += 2
		}
	}

	var rows []vectorSearchRow
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("weave: pgvector search: %w", err)
	}

	results := make([]vectorstore.SearchResult, 0, len(rows))
	for _, row := range rows {
		score := 1 - row.Distance // Convert cosine distance to similarity.
		if opts != nil && opts.MinScore > 0 && score < opts.MinScore {
			continue
		}
		results = append(results, vectorstore.SearchResult{
			Entry: vectorstore.Entry{
				ID:       row.ID,
				Vector:   row.Vector,
				Content:  row.Content,
				Metadata: row.Metadata,
			},
			Score: score,
		})
	}
	return results, nil
}

// Delete removes entries by their IDs.
func (s *Store) Delete(ctx context.Context, ids []string) error {
	_, err := s.pg.NewDelete((*VectorEntry)(nil)).
		Where("id = ANY(?)", pgdriver.StringArray(ids)).
		Exec(ctx)
	return err
}

// DeleteByMetadata removes entries matching the given metadata filter.
func (s *Store) DeleteByMetadata(ctx context.Context, filter map[string]string) error {
	q := s.pg.NewDelete((*VectorEntry)(nil))
	for k, v := range filter {
		q = q.Where("metadata->>? = ?", k, v)
	}
	_, err := q.Exec(ctx)
	return err
}

// vectorToString converts a float32 slice to pgvector literal format: '[0.1,0.2,0.3]'.
func vectorToString(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = fmt.Sprintf("%g", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
