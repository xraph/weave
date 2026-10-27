// Package migrateretry runs grove migration groups on PostgreSQL and
// retries the one failure replicas booting together can hit.
//
// Grove v1.10.0 creates grove_migrations and grove_migration_locks before it
// takes the migration lock, so replicas starting at once on a database
// without those tables race on the CREATE, and the losers fail with
// duplicate_table or duplicate_object. Run retries that failure a few times
// with a short jittered pause. By the next attempt the tables exist and
// grove's lock serialises the replicas.
package migrateretry

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/xraph/grove/migrate"
)

// Attempts bounds how many times Run runs the orchestrator.
const Attempts = 5

// Run applies the pending migrations of groups through grove's orchestrator
// on exec, retrying a lost race on grove's tracking tables up to Attempts
// times. Any other error is returned at once.
func Run(ctx context.Context, exec migrate.Executor, groups ...*migrate.Group) error {
	var err error
	for attempt := 1; ; attempt++ {
		_, err = migrate.NewOrchestrator(exec, groups...).Migrate(ctx)
		if err == nil {
			return nil
		}
		if attempt == Attempts || !IsConcurrentCreate(err) {
			return err
		}
		if waitErr := pause(ctx, backoff(attempt)); waitErr != nil {
			return errors.Join(err, waitErr)
		}
	}
}

// IsConcurrentCreate reports duplicate_table (42P07) or duplicate_object
// (42710), what Postgres raises when two sessions run the same CREATE TABLE
// IF NOT EXISTS at once and one loses. The string check covers a wrapper
// that flattens the PgError.
func IsConcurrentCreate(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P07" || pgErr.Code == "42710"
	}
	return err != nil && (strings.Contains(err.Error(), "SQLSTATE 42P07") ||
		strings.Contains(err.Error(), "SQLSTATE 42710"))
}

// backoff returns the pause before the next attempt: a base that grows with
// each attempt plus up to as much again of jitter, so racing replicas spread
// out. Attempt 1 waits 50 to 100ms, attempt 4 at most 400ms.
func backoff(attempt int) time.Duration {
	base := time.Duration(attempt) * 50 * time.Millisecond
	return base + jitter(base)
}

// jitter returns a random duration in [0, limit).
func jitter(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return limit / 2
	}
	return time.Duration(n.Int64())
}

// pause waits for d or until ctx is done, whichever comes first.
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
