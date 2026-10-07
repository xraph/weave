package fabriqvec

import "github.com/xraph/weave"

var _ weave.Describer = (*Store)(nil)

// Describe reports the fabriq store. Its score is whatever fabriq returns,
// and how fabriq applies a tenant_id filter of "" has not been checked.
func (s *Store) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "fabriq", Score: weave.ScoreVectorSimilarity, TenantFilter: "unverified", Params: map[string]string{"entity": s.entity}}
}
