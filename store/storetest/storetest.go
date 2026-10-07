// Package storetest is the conformance suite every Weave store backend runs.
//
// Each case populates the fields the dashboard reads (metadata, offsets,
// timestamps, tenants), because a suite that only builds empty structs tests
// the absence of those features. Assertions compare identities, never bare
// counts: a count passes when the wrong rows come back in the right number.
package storetest

import (
	"testing"

	"github.com/xraph/weave/store"
)

// Opener returns a fresh, migrated, empty store. It is called once per case.
type Opener func(t *testing.T) store.Store

// Run runs every conformance case against the backend open returns.
func Run(t *testing.T, open Opener) {
	t.Run("Timestamps", func(t *testing.T) { testTimestamps(t, open(t)) })
	t.Run("Filters", func(t *testing.T) { testFilters(t, open(t)) })
	t.Run("Ordering", func(t *testing.T) { testOrdering(t, open(t)) })
	t.Run("Duplicates", func(t *testing.T) { testDuplicates(t, open(t)) })
	t.Run("Tenancy", func(t *testing.T) { testTenancy(t, open(t)) })
	t.Run("Stalled", func(t *testing.T) { testStalled(t, open(t)) })
	t.Run("Chunks", func(t *testing.T) { testChunks(t, open(t)) })
	t.Run("NilMetadata", func(t *testing.T) { testNilMetadata(t, open(t)) })
}
