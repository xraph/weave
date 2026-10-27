package pgvector

import "github.com/xraph/weave/vectorstore"

// ReasonFor and VersionReason expose Available's wording to the tests.
var (
	ReasonFor     = reasonFor
	VersionReason = versionReason
)

// ProbeAnswer exposes how Available sorts a failed probe into a reason or
// an error.
var ProbeAnswer = probeAnswer

// SupportsIterativeScan reports whether pgvector version has
// hnsw.iterative_scan.
var SupportsIterativeScan = supportsIterativeScan

// SkipIterativeScan makes s search without SET LOCAL hnsw.iterative_scan,
// as a pgvector older than 0.8.0 would, so a test can show what the setting
// changes.
func SkipIterativeScan(s *Store) *Store {
	s.skipIterativeScan = true
	return s
}

// SearchQuery returns the statement and arguments Search runs, for EXPLAIN.
func SearchQuery(s *Store, vector []float32, opts *vectorstore.SearchOptions) (string, []any) {
	return s.searchQuery(vector, opts)
}
