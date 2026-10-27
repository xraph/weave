package pgtest

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaWithVector is Schema for a test that needs the pgvector extension.
// It creates the extension in public once per database and pins search_path
// to the new schema, then public, so the vector type resolves while the
// test's tables still land in its own schema.
//
// The extension lives in public rather than the test schema because dropping
// a schema that holds it would drop it for every other test on the database.
// Two packages creating it at once can collide on pg_extension's unique
// index (SQLSTATE 23505); the loser finds it there and carries on.
func SchemaWithVector(t testing.TB, dsn string) string {
	t.Helper()

	refused, err := usesDefaultPort(dsn)
	if err != nil {
		t.Fatalf("parse WEAVE_TEST_PGVECTOR_DSN: %v", err)
	}
	if refused {
		t.Fatalf("WEAVE_TEST_PGVECTOR_DSN points at the default port; refusing to write to what may be a live database")
	}

	const stmt = "CREATE EXTENSION IF NOT EXISTS vector SCHEMA public"
	if err := execErr(dsn, stmt); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	return schema(t, dsn, "public")
}

// Database creates a randomly named database on the server dsn names, drops
// it when the test ends, and returns dsn pointed at it. A fresh database has
// no extensions, so a test can see pgvector as available but not installed.
func Database(t testing.TB, dsn string) string {
	t.Helper()

	refused, err := usesDefaultPort(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	if refused {
		t.Fatalf("test DSN points at the default port; refusing to write to what may be a live database")
	}

	name := "weave_test_" + randomSuffix(t)
	quoted := pgx.Identifier{name}.Sanitize()
	exec(t, dsn, "CREATE DATABASE "+quoted)
	t.Cleanup(func() { exec(t, dsn, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)") })

	scoped, err := withDatabase(dsn, name)
	if err != nil {
		t.Fatalf("point test DSN at database %s: %v", name, err)
	}
	return scoped
}

// withDatabase sets the database in whichever DSN spelling pgx was handed.
func withDatabase(dsn, name string) (string, error) {
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return dsn + " dbname=" + name, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}
