// Package storetest is the conformance suite every Weave store backend runs.
//
// Each case populates the fields the dashboard reads (metadata, offsets,
// timestamps, tenants), because a suite that only builds empty structs tests
// the absence of those features. Assertions compare identities, never bare
// counts: a count passes when the wrong rows come back in the right number.
package storetest

import (
	"testing"

	"github.com/xraph/weave/document"
	"github.com/xraph/weave/store"
)

// Opener returns a fresh, migrated, empty store. It is called once per case.
type Opener func(t *testing.T) store.Store

// Planter writes doc straight into s's storage, with none of the store's
// checks, the way a v1.8 install wrote rows with an empty tenant. It only
// exists for the cases RunLegacy runs; the store API can't write such a row.
type Planter func(t *testing.T, s store.Store, doc *document.Document)

// RunLegacy runs the cases that need rows only an older install could
// write, planted with plant. A custom store that never held v1.8 data can
// skip it.
func RunLegacy(t *testing.T, open Opener, plant Planter) {
	t.Run("StalledSkipsUntenanted", func(t *testing.T) { testStalledSkipsUntenanted(t, open(t), plant) })
}

// Run runs every conformance case against the backend open returns.
func Run(t *testing.T, open Opener) {
	t.Run("Timestamps", func(t *testing.T) { testTimestamps(t, open(t)) })
	t.Run("Filters", func(t *testing.T) { testFilters(t, open(t)) })
	t.Run("Ordering", func(t *testing.T) { testOrdering(t, open(t)) })
	t.Run("Duplicates", func(t *testing.T) { testDuplicates(t, open(t)) })
	t.Run("TenantScoping", func(t *testing.T) { testTenantScoping(t, open(t)) })
	t.Run("TenantRequired", func(t *testing.T) { testTenantRequired(t, open(t)) })
	t.Run("TenantFilterMustMatch", func(t *testing.T) { testTenantFilterMustMatch(t, open(t)) })
	t.Run("TenantParents", func(t *testing.T) { testTenantParents(t, open(t)) })
	t.Run("Stalled", func(t *testing.T) { testStalled(t, open(t)) })
	t.Run("ListStalledSpansTenants", func(t *testing.T) { testListStalledSpansTenants(t, open(t)) })
	t.Run("TouchDocument", func(t *testing.T) { testTouchDocument(t, open(t)) })
	t.Run("FailDocument", func(t *testing.T) { testFailDocument(t, open(t)) })
	t.Run("ClaimOnce", func(t *testing.T) { testClaimOnce(t, open(t)) })
	t.Run("Chunks", func(t *testing.T) { testChunks(t, open(t)) })
	t.Run("NilMetadata", func(t *testing.T) { testNilMetadata(t, open(t)) })
	t.Run("SourceReplace", func(t *testing.T) { testSourceReplace(t, open(t)) })
	t.Run("SourceReplaceRefuses", func(t *testing.T) { testSourceReplaceRefuses(t, open(t)) })
	t.Run("SourceReplaceAtomic", func(t *testing.T) { testSourceReplaceAtomic(t, open(t)) })
	t.Run("SourceReplaceSharedChunkID", func(t *testing.T) { testSourceReplaceSharedChunkID(t, open(t)) })
	t.Run("OneReadyPerSource", func(t *testing.T) { testOneReadyDocumentPerSource(t, open(t)) })
	t.Run("SourceKeyFilter", func(t *testing.T) { testSourceKeyFilter(t, open(t)) })
}
