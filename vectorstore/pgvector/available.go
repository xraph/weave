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
// EXTENSION IF NOT EXISTS vector inside a transaction it always rolls back,
// so a host can call it at boot to decide whether to turn knowledge on
// without making pgvector a requirement. Replicas booting together may run
// it while another replica's Migrate is creating the extension: the probe
// then loses on pg_extension's unique index, and Available reads the
// committed extension instead of reporting a refusal. A lock or statement
// timeout, a cancellation or a deadlock is an error, not a reason.
func Available(ctx context.Context, db *grove.DB) (ok bool, reason string, err error) {
	pg, isPG := db.Driver().(*pgdriver.PgDB)
	if !isPG {
		return false, "the database is not PostgreSQL", nil
	}

	installed, found, err := installedVersion(ctx, pg)
	if err != nil {
		return false, "", err
	}
	if found {
		return versionAnswer(installed)
	}

	rows, err := pg.Query(ctx, `SELECT default_version FROM pg_available_extensions WHERE name = 'vector'`)
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

	raced, err := tryCreate(ctx, pg)
	if err != nil || !raced {
		return probeAnswer(err)
	}

	// Another session created the extension while the probe ran. Its
	// commit is visible now.
	installed, found, err = installedVersion(ctx, pg)
	if err != nil {
		return false, "", err
	}
	if !found {
		return false, "", errors.New("weave: probe the vector extension: another session was creating it; ask again")
	}
	return versionAnswer(installed)
}

// versionAnswer is Available's answer for an installed version.
func versionAnswer(version string) (ok bool, reason string, err error) {
	reason = versionReason(version)
	return reason == "", reason, nil
}

// installedVersion reads the installed vector extension's version.
func installedVersion(ctx context.Context, pg *pgdriver.PgDB) (version string, found bool, err error) {
	rows, err := pg.Query(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`)
	if err != nil {
		return "", false, fmt.Errorf("weave: read the vector extension version: %w", err)
	}
	version, found, err = firstString(rows)
	if err != nil {
		return "", false, fmt.Errorf("weave: read the vector extension version: %w", err)
	}
	return version, found, nil
}

// tryCreate runs CREATE EXTENSION IF NOT EXISTS vector on one transaction
// and rolls it back whatever happens. raced is true when the create lost to
// another session creating the extension at the same moment (23505, or
// 42710); any other failure is returned as err for probeAnswer to sort.
func tryCreate(ctx context.Context, pg *pgdriver.PgDB) (raced bool, err error) {
	tx, err := pg.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("weave: probe the vector extension: %w", err)
	}
	defer rollback(tx)

	if _, err = tx.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS vector`); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && (pgErr.Code == "23505" || pgErr.Code == "42710") {
			return true, nil
		}
		return false, fmt.Errorf("weave: probe the vector extension: %w", err)
	}
	return false, nil
}

// probeAnswer turns the probe's outcome into Available's answer. A refusal
// from the server becomes a reason. A failure to ask, which includes a
// transient server error that says nothing about pgvector, is an error.
func probeAnswer(err error) (ok bool, reason string, _ error) {
	if err == nil {
		return true, "", nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || isTransient(pgErr.Code) {
		return false, "", err
	}
	return false, reasonFor(pgErr), nil
}

// isTransient reports a SQLSTATE that means the probe could not finish, not
// that pgvector is unusable: a lost connection (class 08), a serialization
// failure or deadlock (class 40), lock_not_available from lock_timeout
// (55P03), and a cancellation, statement timeout or shutdown (class 57).
func isTransient(code string) bool {
	if code == "55P03" {
		return true
	}
	switch code[:min(2, len(code))] {
	case "08", "40", "57":
		return true
	}
	return false
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
