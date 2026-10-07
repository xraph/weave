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
