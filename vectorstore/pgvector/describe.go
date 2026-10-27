package pgvector

import (
	"strconv"

	"github.com/xraph/weave"
)

var _ weave.Describer = (*Store)(nil)

// Describe reports the pgvector store. It scores 1 - cosine distance, and
// every search filters metadata->>'tenant_id' = TenantKey, which Search
// refuses to run empty.
func (s *Store) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{
		Kind:         "pgvector",
		Score:        weave.ScoreCosine,
		TenantFilter: "verified",
		Params:       s.describeParams(),
	}
}

// describeParams names the table, and the dimensions when WithDimensions set
// them.
func (s *Store) describeParams() map[string]string {
	p := map[string]string{"table": s.tableName}
	if s.dimensions > 0 {
		p["dimensions"] = strconv.Itoa(s.dimensions)
	}
	return p
}
