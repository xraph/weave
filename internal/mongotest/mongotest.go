// Package mongotest gives each test that writes to a live MongoDB a database
// of its own, and drops it when the test ends.
package mongotest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Database returns dsn with its database replaced by a random one, and
// registers a cleanup that drops that database. It refuses the default port.
func Database(t testing.TB, dsn string) string {
	t.Helper()

	if strings.Contains(dsn, ":27017") {
		t.Fatalf("WEAVE_TEST_MONGO_DSN points at the default port; refusing to write to what may be a live database")
	}

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse WEAVE_TEST_MONGO_DSN: %v", err)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("random database suffix: %v", err)
	}
	name := "weave_test_" + hex.EncodeToString(b[:])
	u.Path = "/" + name
	scoped := u.String()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		client, err := mongo.Connect(options.Client().ApplyURI(scoped))
		if err != nil {
			t.Logf("mongotest: connect to drop %s: %v", name, err)
			return
		}
		defer func() {
			if err := client.Disconnect(ctx); err != nil {
				t.Logf("mongotest: disconnect after dropping %s: %v", name, err)
			}
		}()
		if err := client.Database(name).Drop(ctx); err != nil {
			t.Logf("mongotest: drop %s: %v", name, err)
		}
	})
	return scoped
}
