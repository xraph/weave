package postgres

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// isUniqueViolation reports a Postgres unique_violation (23505). grove wraps
// pgx errors with %w, so errors.As finds the PgError; the string check covers
// a wrapper that flattens it.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return err != nil && strings.Contains(err.Error(), "SQLSTATE 23505")
}

// isConcurrentCreate reports duplicate_table (42P07) or duplicate_object
// (42710), what Postgres raises when two sessions run the same CREATE TABLE
// IF NOT EXISTS at once and one loses. The string check covers a wrapper
// that flattens the PgError.
func isConcurrentCreate(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P07" || pgErr.Code == "42710"
	}
	return err != nil && (strings.Contains(err.Error(), "SQLSTATE 42P07") ||
		strings.Contains(err.Error(), "SQLSTATE 42710"))
}
