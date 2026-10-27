package pgvector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xraph/grove"
	"github.com/xraph/grove/driver"
	"github.com/xraph/grove/drivers/pgdriver"
)

// minVersion is the oldest pgvector weave runs on: HNSW arrived in 0.5.0.
const minVersion = "0.5.0"

// iterativeScanVersion is the first pgvector with hnsw.iterative_scan.
const iterativeScanVersion = "0.8.0"

const notInstalled = "the vector extension is not installed on this PostgreSQL server"

// Available reports whether weave can use pgvector on db: true when the
// vector extension (0.5.0 or later) is installed or can be created by this
// role. When false, reason says why in one sentence. err is for failures to
// ask (a connection error), never for "no". It changes nothing.
//
// To learn whether the role may create the extension, Available runs CREATE
// EXTENSION vector inside a transaction it always rolls back, so a host can
// call it at boot to decide whether to turn knowledge on without making
// pgvector a requirement.
func Available(ctx context.Context, db *grove.DB) (ok bool, reason string, err error) {
	pg, isPG := db.Driver().(*pgdriver.PgDB)
	if !isPG {
		return false, "the database is not PostgreSQL", nil
	}

	rows, err := pg.Query(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`)
	if err != nil {
		return false, "", fmt.Errorf("weave: read the vector extension version: %w", err)
	}
	installed, found, err := firstString(rows)
	if err != nil {
		return false, "", fmt.Errorf("weave: read the vector extension version: %w", err)
	}
	if found {
		if r := versionReason(installed); r != "" {
			return false, r, nil
		}
		return true, "", nil
	}

	rows, err = pg.Query(ctx, `SELECT default_version FROM pg_available_extensions WHERE name = 'vector'`)
	if err != nil {
		return false, "", fmt.Errorf("weave: list available extensions: %w", err)
	}
	available, found, err := firstString(rows)
	if err != nil {
		return false, "", fmt.Errorf("weave: list available extensions: %w", err)
	}
	if !found {
		return false, notInstalled, nil
	}
	if r := versionReason(available); r != "" {
		return false, r, nil
	}

	return tryCreate(ctx, pg)
}

// tryCreate runs CREATE EXTENSION vector on one transaction and rolls it
// back whatever happens. A refusal from the server becomes a reason; a
// failure to talk to it is an error.
func tryCreate(ctx context.Context, pg *pgdriver.PgDB) (ok bool, reason string, err error) {
	tx, err := pg.BeginTx(ctx, nil)
	if err != nil {
		return false, "", fmt.Errorf("weave: probe the vector extension: %w", err)
	}
	defer rollback(tx)

	if _, err = tx.Exec(ctx, `CREATE EXTENSION vector`); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return false, reasonFor(pgErr), nil
		}
		return false, "", fmt.Errorf("weave: probe the vector extension: %w", err)
	}
	return true, "", nil
}

// reasonFor turns the server's refusal of CREATE EXTENSION vector into a
// sentence for an operator. It reads only the SQLSTATE, never the message,
// which can name the server's paths.
func reasonFor(pgErr *pgconn.PgError) string {
	switch pgErr.Code {
	case "42501":
		return "this database role is not allowed to create the vector extension; ask a database owner to run CREATE EXTENSION vector"
	case "58P01", "0A000":
		return notInstalled
	default:
		return "CREATE EXTENSION vector failed with SQLSTATE " + pgErr.Code
	}
}

// versionReason is "" when version is minVersion or later, and otherwise
// says weave needs a newer pgvector.
func versionReason(version string) string {
	if versionAtLeast(version, minVersion) {
		return ""
	}
	return "the vector extension is version " + version + "; weave needs " + minVersion + " or later for HNSW indexes"
}

// supportsIterativeScan reports whether pgvector version has
// hnsw.iterative_scan.
func supportsIterativeScan(version string) bool {
	return versionAtLeast(version, iterativeScanVersion)
}

// versionAtLeast compares dotted versions numerically, part by part. A part
// that is not a number, or a suffix such as "-dev", counts as what its
// leading digits say.
func versionAtLeast(version, want string) bool {
	have, need := versionParts(version), versionParts(want)
	for i := range need {
		var h int
		if i < len(have) {
			h = have[i]
		}
		if h != need[i] {
			return h > need[i]
		}
	}
	return true
}

func versionParts(version string) []int {
	fields := strings.Split(version, ".")
	parts := make([]int, len(fields))
	for i, f := range fields {
		for _, c := range f {
			if c < '0' || c > '9' {
				break
			}
			parts[i] = parts[i]*10 + int(c-'0')
		}
	}
	return parts
}

// firstString reads the first column of the first row, if there is one,
// and closes rows.
func firstString(rows driver.Rows) (value string, found bool, err error) {
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return "", false, rows.Err()
	}
	if scanErr := rows.Scan(&value); scanErr != nil {
		return "", false, scanErr
	}
	return value, true, rows.Err()
}
