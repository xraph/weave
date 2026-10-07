package pgvector

import "github.com/xraph/weave"

var _ weave.Describer = (*Store)(nil)

// Describe reports the pgvector store. It scores 1 - cosine distance, and
// filters metadata with metadata->>'tenant_id' = $n, which matches "".
func (s *Store) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "pgvector", Score: weave.ScoreCosine, TenantFilter: "verified", Params: map[string]string{"table": s.tableName}}
}
