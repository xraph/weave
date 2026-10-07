package sqlite

import "strings"

// isUniqueViolation reports a SQLite UNIQUE constraint failure. modernc's
// error text for it is stable and carries no typed code through grove.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
