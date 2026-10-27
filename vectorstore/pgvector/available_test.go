package pgvector_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xraph/grove/drivers/pgdriver"

	"github.com/xraph/weave/internal/pgtest"
	"github.com/xraph/weave/vectorstore/pgvector"
)

func TestAvailabilityReasons(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"42501", "not allowed"},
		{"58P01", "not installed"},
		{"0A000", "not installed"},
		{"XX000", "SQLSTATE XX000"},
	}
	for _, tc := range cases {
		if got := pgvector.ReasonFor(&pgconn.PgError{Code: tc.code}); !strings.Contains(got, tc.want) {
			t.Errorf("reasonFor(%s) = %q, want it to contain %q", tc.code, got, tc.want)
		}
	}

	if got := pgvector.VersionReason("0.4.4"); !strings.Contains(got, "0.5.0") {
		t.Errorf("versionReason(0.4.4) = %q, want it to name 0.5.0", got)
	}
	for _, v := range []string{"0.5.0", "0.8.1", "1.0.0"} {
		if got := pgvector.VersionReason(v); got != "" {
			t.Errorf("versionReason(%s) = %q, want empty", v, got)
		}
	}
}

func TestAvailableWhenInstalled(t *testing.T) {
	ctx, db := openVectorDB(t)
	ok, reason, err := pgvector.Available(ctx, db)
	if !ok || reason != "" || err != nil {
		t.Fatalf("Available = (%v, %q, %v), want (true, \"\", nil)", ok, reason, err)
	}
}

// TestAvailableWhenCreatable runs Available on a fresh database of the
// pgvector server, where the extension is available but not installed. It
// answers yes and leaves the database without the extension.
func TestAvailableWhenCreatable(t *testing.T) {
	ctx, dsn := freshVectorDatabase(t)
	db := openDB(ctx, t, dsn)

	ok, reason, err := pgvector.Available(ctx, db)
	if !ok || reason != "" || err != nil {
		t.Fatalf("Available = (%v, %q, %v), want (true, \"\", nil)", ok, reason, err)
	}
	assertNoVectorExtension(ctx, t, pgdriver.Unwrap(db))
}

// TestAvailableWithoutPrivilege connects as a role that owns nothing and is
// not a superuser, on a database without the extension. CREATE EXTENSION
// vector fails with 42501, and Available says so without creating anything.
func TestAvailableWithoutPrivilege(t *testing.T) {
	ctx, dsn := freshVectorDatabase(t)
	roleDSN := plainRole(ctx, t, dsn)
	db := openDB(ctx, t, roleDSN)

	ok, reason, err := pgvector.Available(ctx, db)
	if ok || err != nil || !strings.Contains(reason, "not allowed") {
		t.Fatalf("Available = (%v, %q, %v), want (false, a reason containing \"not allowed\", nil)", ok, reason, err)
	}
	assertNoVectorExtension(ctx, t, pgdriver.Unwrap(db))
}

func TestAvailableOnPlainPostgres(t *testing.T) {
	dsn := os.Getenv("WEAVE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_POSTGRES_DSN not set, skipping postgres")
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	db := openDB(ctx, t, pgtest.Schema(t, dsn))

	ok, reason, err := pgvector.Available(ctx, db)
	if ok || err != nil || !strings.Contains(reason, "not installed") {
		t.Fatalf("Available = (%v, %q, %v), want (false, a reason containing \"not installed\", nil)", ok, reason, err)
	}
	assertNoVectorExtension(ctx, t, pgdriver.Unwrap(db))
}

func TestAvailableReportsAConnectionFailure(t *testing.T) {
	ctx, db := openVectorDB(t)
	_ = db.Close()

	ok, reason, err := pgvector.Available(ctx, db)
	if ok || reason != "" || err == nil {
		t.Fatalf("Available on a closed database = (%v, %q, %v), want (false, \"\", an error)", ok, reason, err)
	}
}

func assertNoVectorExtension(ctx context.Context, t *testing.T, pg *pgdriver.PgDB) {
	t.Helper()
	var n int
	if err := pg.QueryRow(ctx, `SELECT count(*) FROM pg_extension WHERE extname = 'vector'`).Scan(&n); err != nil {
		t.Fatalf("count vector extension: %v", err)
	}
	if n != 0 {
		t.Fatalf("pg_extension has %d vector rows, want 0: Available must change nothing", n)
	}
}

// freshVectorDatabase returns a DSN naming a database of its own on the
// pgvector server, where the vector extension is available but not
// installed.
func freshVectorDatabase(t *testing.T) (context.Context, string) {
	t.Helper()
	dsn := os.Getenv("WEAVE_TEST_PGVECTOR_DSN")
	if dsn == "" {
		t.Skip("WEAVE_TEST_PGVECTOR_DSN not set, skipping pgvector")
	}
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	return ctx, pgtest.Database(t, dsn)
}

// plainRole creates a login role with no privileges beyond connecting, drops
// it when the test ends, and returns dsn rewritten to log in as it.
func plainRole(ctx context.Context, t *testing.T, dsn string) string {
	t.Helper()
	role := "weave_test_" + randomHex(t)
	password := randomHex(t)
	ident := pgx.Identifier{role}.Sanitize()
	adminExec(ctx, t, dsn, "CREATE ROLE "+ident+" LOGIN NOSUPERUSER PASSWORD '"+password+"'")
	t.Cleanup(func() { adminExec(context.Background(), t, dsn, "DROP ROLE IF EXISTS "+ident) })

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.User = url.UserPassword(role, password)
	return u.String()
}

func adminExec(ctx context.Context, t *testing.T, dsn, stmt string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

func randomHex(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("random: %v", err)
	}
	return hex.EncodeToString(b[:])
}
