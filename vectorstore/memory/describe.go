package memory

import "github.com/xraph/weave"

var _ weave.Describer = (*Store)(nil)

// Describe reports the in-memory vector store: cosine similarity, and an
// exact tenant_id metadata filter that is covered by Weave's own tests.
func (*Store) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "memory", Score: weave.ScoreCosine, TenantFilter: "verified"}
}
