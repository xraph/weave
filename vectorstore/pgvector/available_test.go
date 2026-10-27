package pgvector_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xraph/grove"
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

// TestProbeFailuresSplitIntoReasonsAndErrors checks that only a refusal
// becomes a reason. A lock timeout, a cancellation, a deadlock or a lost
// connection says nothing about pgvector and comes back as an error.
func TestProbeFailuresSplitIntoReasonsAndErrors(t *testing.T) {
	for _, code := range []string{"42501", "58P01", "0A000", "XX000"} {
		ok, reason, err := pgvector.ProbeAnswer(&pgconn.PgError{Code: code})
		if ok || reason == "" || err != nil {
			t.Errorf("probe failed with %s: (%v, %q, %v), want a reason", code, ok, reason, err)
		}
	}
	for _, code := range []string{"55P03", "57014", "57P01", "40P01", "40001", "08006"} {
		ok, reason, err := pgvector.ProbeAnswer(&pgconn.PgError{Code: code})
		if ok || reason != "" || err == nil {
			t.Errorf("probe failed with %s: (%v, %q, %v), want an error", code, ok, reason, err)
		}
	}
	if ok, reason, err := pgvector.ProbeAnswer(errors.New("conn closed")); ok || reason != "" || err == nil {
		t.Errorf("probe failed without a SQLSTATE: (%v, %q, %v), want an error", ok, reason, err)
	}
}

// TestAvailableWhileAnotherSessionCreates holds an uncommitted CREATE
// EXTENSION on a fresh database, the way a replica inside Migrate does,
// starts Available, waits until its probe is blocked behind that session,
// then commits. The probe loses on pg_extension's unique index, and
// Available must still answer yes.
func TestAvailableWhileAnotherSessionCreates(t *testing.T) {
	ctx, dsn := freshVectorDatabase(t)
	holder := holdCreateExtension(ctx, t, dsn)
	db := openDB(ctx, t, dsn)

	type answer struct {
		ok     bool
		reason string
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		ok, reason, err := pgvector.Available(ctx, db)
		done <- answer{ok, reason, err}
	}()
	waitForBlockedProbe(ctx, t, dsn)
	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit the held CREATE EXTENSION: %v", err)
	}

	got := <-done
	if !got.ok || got.reason != "" || got.err != nil {
		t.Fatalf("Available racing a committed CREATE EXTENSION = (%v, %q, %v), want (true, \"\", nil)", got.ok, got.reason, got.err)
	}
}

// TestAvailableLockTimeoutIsAnError runs Available with lock_timeout set
// while another session holds an uncommitted CREATE EXTENSION. The probe
// gives up with 55P03, which is a failure to ask, not a "no".
func TestAvailableLockTimeoutIsAnError(t *testing.T) {
	ctx, dsn := freshVectorDatabase(t)
	holder := holdCreateExtension(ctx, t, dsn)
	defer func() { _ = holder.Rollback(ctx) }()
	db := openDB(ctx, t, withSetting(t, dsn, "lock_timeout", "200"))

	ok, reason, err := pgvector.Available(ctx, db)
	var pgErr *pgconn.PgError
	if ok || reason != "" || !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("Available behind a held CREATE EXTENSION with lock_timeout = (%v, %q, %v), want (false, \"\", a 55P03 error)", ok, reason, err)
	}
}

// TestFirstBootAvailableThenMigrate follows the documented boot on a fresh
// database without the extension: eight replicas at once, each asking
// Available and then running Migrate. No replica may be told no, and every
// Migrate must succeed. Each round uses a fresh database, because the race
// only exists until the extension is installed.
func TestFirstBootAvailableThenMigrate(t *testing.T) {
	const rounds, replicas = 4, 8
	for round := range rounds {
		ctx, dsn := freshVectorDatabase(t)
		dbs := make([]*grove.DB, replicas)
		for i := range dbs {
			dbs[i] = openDB(ctx, t, dsn)
		}

		var wg sync.WaitGroup
		for i, db := range dbs {
			wg.Go(func() {
				ok, reason, err := pgvector.Available(ctx, db)
				if !ok || err != nil {
					t.Errorf("round %d replica %d: Available = (%v, %q, %v), want yes", round, i, ok, reason, err)
					return
				}
				if err := pgvector.Migrate(ctx, db, 8); err != nil {
					t.Errorf("round %d replica %d: Migrate: %v", round, i, err)
				}
			})
		}
		wg.Wait()
		if t.Failed() {
			t.FailNow()
		}
		if got := appliedVersions(ctx, t, pgdriver.Unwrap(dbs[0])); len(got) != 1 {
			t.Fatalf("round %d: applied weave_pgvector versions = %v, want one", round, got)
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

// holdCreateExtension creates the vector extension on dsn in a transaction
// it leaves open, and rolls it back at cleanup unless the test committed.
func holdCreateExtension(ctx context.Context, t *testing.T, dsn string) pgx.Tx {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect holder: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, "CREATE EXTENSION vector"); err != nil {
		t.Fatalf("held CREATE EXTENSION: %v", err)
	}
	return tx
}

// waitForBlockedProbe waits until a session on dsn's database is waiting on
// a lock inside Available's CREATE EXTENSION probe.
func waitForBlockedProbe(ctx context.Context, t *testing.T, dsn string) {
	t.Helper()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect watcher: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND wait_event_type = 'Lock'
  AND query LIKE 'CREATE EXTENSION IF NOT EXISTS vector%'`).Scan(&n); err != nil {
			t.Fatalf("watch pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("Available's probe never blocked behind the held CREATE EXTENSION")
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
