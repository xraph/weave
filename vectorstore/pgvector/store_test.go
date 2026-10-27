package pgvector_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xraph/grove"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/internal/embedtest"
	"github.com/xraph/weave/internal/pgtest"
	"github.com/xraph/weave/store/postgres"
	"github.com/xraph/weave/vectorstore"
	"github.com/xraph/weave/vectorstore/pgvector"
)

const testTimeout = 120 * time.Second

func TestUpsertSearchDeleteHonourTableName(t *testing.T) {
	ctx, db := openVectorDB(t)
	pg := pgdriver.Unwrap(db)
	if err := pgvector.Migrate(ctx, db, 8); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := pg.Exec(ctx, `CREATE TABLE kb_vectors (
    id        TEXT PRIMARY KEY,
    embedding vector(8) NOT NULL,
    content   TEXT NOT NULL DEFAULT '',
    metadata  JSONB NOT NULL DEFAULT '{}'
)`); err != nil {
		t.Fatalf("create kb_vectors: %v", err)
	}

	s := pgvector.New(db, pgvector.WithTableName("kb_vectors"), pgvector.WithDimensions(8))
	if err := s.CheckDimensions(ctx); err != nil {
		t.Fatalf("check kb_vectors: %v", err)
	}
	entries := []vectorstore.Entry{
		{ID: "a", Vector: unit(0), Content: "alpha", Metadata: map[string]string{"tenant_id": "t1", "document_id": "d1"}},
		{ID: "b", Vector: unit(1), Content: "beta", Metadata: map[string]string{"tenant_id": "t1", "document_id": "d1"}},
		{ID: "c", Vector: unit(2), Content: "gamma", Metadata: map[string]string{"tenant_id": "t1", "document_id": "d2"}},
	}
	if err := s.Upsert(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	assertCount(ctx, t, pg, "kb_vectors", 3)
	assertCount(ctx, t, pg, "weave_vectors", 0)

	// A second upsert of the same ID replaces it.
	entries[0].Content = "alpha two"
	if err := s.Upsert(ctx, entries[:1]); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	assertCount(ctx, t, pg, "kb_vectors", 3)

	hits, err := s.Search(ctx, unit(0), &vectorstore.SearchOptions{TopK: 3, TenantKey: "t1"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 3 || hits[0].ID != "a" || hits[0].Content != "alpha two" || hits[0].Metadata["document_id"] != "d1" {
		t.Fatalf("search: %+v, want a (alpha two, d1) first of 3", hits)
	}
	if hits[0].Score < 0.999 || len(hits[0].Vector) != 8 || hits[0].Vector[0] != 1 {
		t.Errorf("top hit: score %v vector %v, want 1 and the stored vector", hits[0].Score, hits[0].Vector)
	}
	filtered, err := s.Search(ctx, unit(0), &vectorstore.SearchOptions{TopK: 3, TenantKey: "t1", Filter: map[string]string{"document_id": "d2"}})
	if err != nil || len(filtered) != 1 || filtered[0].ID != "c" {
		t.Fatalf("filtered search: %+v, %v; want only c", filtered, err)
	}
	if other, err := s.Search(ctx, unit(0), &vectorstore.SearchOptions{TopK: 3, TenantKey: "t2"}); err != nil || len(other) != 0 {
		t.Fatalf("search as t2: %+v, %v; want nothing", other, err)
	}

	if err := s.Delete(ctx, []string{"a"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	assertCount(ctx, t, pg, "kb_vectors", 2)
	if err := s.Delete(ctx, nil); err != nil {
		t.Fatalf("delete nothing: %v", err)
	}
	if err := s.DeleteByMetadata(ctx, map[string]string{"tenant_id": "t1", "document_id": "d2"}); err != nil {
		t.Fatalf("delete by metadata: %v", err)
	}
	assertCount(ctx, t, pg, "kb_vectors", 1)
	assertCount(ctx, t, pg, "weave_vectors", 0)

	if got := s.Describe().Params; got["table"] != "kb_vectors" || got["dimensions"] != "8" {
		t.Errorf("Describe params %v, want table kb_vectors and dimensions 8", got)
	}
}

func TestStoreRefusesUnscopedCalls(t *testing.T) {
	ctx, db := openVectorDB(t)
	pg := pgdriver.Unwrap(db)
	if err := pgvector.Migrate(ctx, db, 8); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := pgvector.New(db, pgvector.WithDimensions(8))
	if err := s.Upsert(ctx, []vectorstore.Entry{{ID: "a", Vector: unit(0), Metadata: map[string]string{"tenant_id": "t1"}}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if _, err := s.Search(ctx, unit(0), &vectorstore.SearchOptions{TopK: 5}); !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("search without TenantKey: %v, want ErrTenantRequired", err)
	}
	if _, err := s.Search(ctx, unit(0), nil); !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("search with nil options: %v, want ErrTenantRequired", err)
	}
	err := s.Upsert(ctx, []vectorstore.Entry{
		{ID: "b", Vector: unit(1), Metadata: map[string]string{"tenant_id": "t1"}},
		{ID: "c", Vector: unit(2), Metadata: map[string]string{"document_id": "d1"}},
	})
	if !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("upsert without tenant_id: %v, want ErrTenantRequired", err)
	}
	assertCount(ctx, t, pg, "weave_vectors", 1)
	if err := s.DeleteByMetadata(ctx, nil); !errors.Is(err, weave.ErrInvalidArgument) {
		t.Errorf("delete by an empty filter: %v, want ErrInvalidArgument", err)
	}
	assertCount(ctx, t, pg, "weave_vectors", 1)
}

// TestSearchFindsSmallTenantInLargeTable puts five vectors of tenant "small"
// behind 3,000 of tenant "big" that sit closer to the query. HNSW filters
// after it walks the graph, and its default ef_search of 40 only ever finds
// big's vectors, so without an iterative scan small gets fewer hits than it
// asked for, or none.
//
// On a table this size Postgres would rather read small's five rows through
// the tenant btree and sort them, which is exact and hides the problem. It
// takes the HNSW path once the table is large and the tenant not tiny. The
// test connects with enable_sort=off to put the query on that path, checks
// with EXPLAIN that it is there, and shows that the same search without the
// iterative scan comes back short.
func TestSearchFindsSmallTenantInLargeTable(t *testing.T) {
	ctx, scoped := vectorSchema(t)
	db := openDB(ctx, t, scoped)
	pg := pgdriver.Unwrap(db)

	var version string
	if err := pg.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`).Scan(&version); err != nil {
		t.Fatalf("read extversion: %v", err)
	}
	if !pgvector.SupportsIterativeScan(version) {
		t.Fatalf("pgvector %s has no iterative scan; this test needs 0.8.0 or later (pull pgvector/pgvector:pg17)", version)
	}
	if err := pgvector.Migrate(ctx, db, 8); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	rng := rand.New(rand.NewPCG(1, 2))
	entries := make([]vectorstore.Entry, 0, 3005)
	for i := range 3000 {
		v := make([]float32, 8)
		v[0] = 1
		for j := 1; j < 8; j++ {
			v[j] = rng.Float32() * 0.3
		}
		entries = append(entries, vectorstore.Entry{ID: fmt.Sprintf("big-%04d", i), Vector: v, Metadata: map[string]string{"tenant_id": "big"}})
	}
	for i := range 5 {
		v := []float32{1, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5, 0.5 + float32(i)*0.01}
		entries = append(entries, vectorstore.Entry{ID: fmt.Sprintf("small-%d", i), Vector: v, Metadata: map[string]string{"tenant_id": "small"}})
	}
	if err := pgvector.New(db, pgvector.WithDimensions(8)).Upsert(ctx, entries); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	assertCount(ctx, t, pg, "weave_vectors", 3005)
	if _, err := pg.Exec(ctx, `ANALYZE weave_vectors`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	hnswDB := openDB(ctx, t, withSetting(t, scoped, "enable_sort", "off"))
	query := unit(0)
	opts := &vectorstore.SearchOptions{TopK: 5, TenantKey: "small"}
	s := pgvector.New(hnswDB, pgvector.WithDimensions(8))

	sql, args := pgvector.SearchQuery(s, query, opts)
	plan := explain(ctx, t, pgdriver.Unwrap(hnswDB), sql, args)
	if !strings.Contains(plan, "idx_weave_vectors_embedding") {
		t.Fatalf("search plan does not use the HNSW index:\n%s", plan)
	}

	control, err := pgvector.SkipIterativeScan(pgvector.New(hnswDB, pgvector.WithDimensions(8))).Search(ctx, query, opts)
	if err != nil {
		t.Fatalf("control search: %v", err)
	}
	if len(control) >= 5 {
		t.Fatalf("control search without the iterative scan: %d hits; the data does not reproduce the problem", len(control))
	}

	hits, err := s.Search(ctx, query, opts)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 5 {
		t.Fatalf("search: %d hits, want 5 (control had %d)", len(hits), len(control))
	}
	for i, h := range hits {
		if h.Metadata["tenant_id"] != "small" {
			t.Errorf("hit %d: %s of tenant %q, want small", i, h.ID, h.Metadata["tenant_id"])
		}
		if i > 0 && h.Score > hits[i-1].Score {
			t.Errorf("hit %d: score %v above hit %d's %v, want descending", i, h.Score, i-1, hits[i-1].Score)
		}
	}
}

// TestEngineOnPostgresAndPgvector runs the engine on the postgres metadata
// store and pgvector in one database: a keyed ingest, a replace with new
// content, and retrieval that sees only the new version and nothing of it
// from another tenant.
func TestEngineOnPostgresAndPgvector(t *testing.T) {
	ctx, db := openVectorDB(t)
	st := postgres.New(db)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate metadata: %v", err)
	}
	if err := pgvector.Migrate(ctx, db, 8); err != nil {
		t.Fatalf("migrate vectors: %v", err)
	}
	vs := pgvector.New(db, pgvector.WithDimensions(8))
	if err := vs.CheckDimensions(ctx); err != nil {
		t.Fatalf("check dimensions: %v", err)
	}
	eng, err := engine.New(
		engine.WithStore(st),
		engine.WithVectorStore(vs),
		engine.WithEmbedder(embedtest.New(8)),
		engine.WithChunker(chunker.NewRecursiveChunker()),
	)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}

	t1 := weave.WithTenant(ctx, "t1")
	t2 := weave.WithTenant(ctx, "t2")
	col := &collection.Collection{Name: "docs", ChunkSize: 512}
	if err = eng.CreateCollection(t1, col); err != nil {
		t.Fatalf("create collection: %v", err)
	}

	const v1 = "refunds are issued within thirty days, version one"
	const v2 = "refunds are issued within thirty days, version two"
	first, err := eng.Ingest(t1, &engine.IngestInput{CollectionID: col.ID, SourceKey: "refunds", Content: v1})
	if err != nil {
		t.Fatalf("ingest v1: %v", err)
	}
	second, err := eng.Ingest(t1, &engine.IngestInput{CollectionID: col.ID, SourceKey: "refunds", Content: v2})
	if err != nil {
		t.Fatalf("ingest v2: %v", err)
	}
	if len(second.Replaced) != 1 || second.Replaced[0].String() != first.DocumentID.String() {
		t.Fatalf("v2 replaced %v, want [%s]", second.Replaced, first.DocumentID)
	}
	assertCount(ctx, t, pgdriver.Unwrap(db), "weave_vectors", second.ChunkCount)

	hits, err := eng.Retrieve(t1, v1, engine.WithCollection(col.ID), engine.WithTopK(20))
	if err != nil {
		t.Fatalf("retrieve as t1: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("retrieve as t1: no hits, want the new version")
	}
	for _, h := range hits {
		if !h.Hydrated || h.Chunk.DocumentID.String() != second.DocumentID.String() {
			t.Errorf("hit %s from %s (hydrated %v), want only %s", h.Chunk.ID, h.Chunk.DocumentID, h.Hydrated, second.DocumentID)
		}
	}

	if _, err := eng.Retrieve(t2, v1, engine.WithCollection(col.ID), engine.WithTopK(20)); !errors.Is(err, weave.ErrCollectionNotFound) {
		t.Errorf("retrieve t1's collection as t2: %v, want ErrCollectionNotFound", err)
	}
	own := &collection.Collection{Name: "docs", ChunkSize: 512}
	if err := eng.CreateCollection(t2, own); err != nil {
		t.Fatalf("create t2 collection: %v", err)
	}
	if other, err := eng.Retrieve(t2, v1, engine.WithCollection(own.ID), engine.WithTopK(20)); err != nil || len(other) != 0 {
		t.Errorf("retrieve as t2: %d hits, %v; want none", len(other), err)
	}
}

// vectorSchema returns a context bounded for one test and a DSN pinned to a
// fresh schema on the pgvector server, with the extension in public.
func vectorSchema(t *testing.T) (context.Context, string) {
	t.Helper()
	dsn := os.Getenv("WEAVE_TEST_PGVECTOR_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_PGVECTOR_DSN not set, skipping pgvector")
	}
	scoped := pgtest.SchemaWithVector(t, dsn)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx, scoped
}

func openVectorDB(t *testing.T) (context.Context, *grove.DB) {
	t.Helper()
	ctx, scoped := vectorSchema(t)
	return ctx, openDB(ctx, t, scoped)
}

func openDB(ctx context.Context, t *testing.T, dsn string) *grove.DB {
	t.Helper()
	drv := pgdriver.New()
	if err := drv.Open(ctx, dsn); err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db, err := grove.Open(drv)
	if err != nil {
		t.Fatalf("grove.Open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// withSetting returns dsn with a server setting every connection starts with.
func withSetting(t *testing.T, dsn, name, value string) string {
	t.Helper()
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return dsn + " " + name + "=" + value
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	q.Set(name, value)
	u.RawQuery = q.Encode()
	return u.String()
}

func explain(ctx context.Context, t *testing.T, pg *pgdriver.PgDB, sql string, args []any) string {
	t.Helper()
	rows, err := pg.Query(ctx, "EXPLAIN "+sql, args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func assertCount(ctx context.Context, t *testing.T, pg *pgdriver.PgDB, table string, want int) {
	t.Helper()
	var n int
	if err := pg.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if n != want {
		t.Fatalf("%s holds %d rows, want %d", table, n, want)
	}
}

// unit returns an 8-number vector with a one at i.
func unit(i int) []float32 {
	v := make([]float32, 8)
	v[i] = 1
	return v
}
