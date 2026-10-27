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
		Params: map[string]string{
			"table":      s.tableName,
			"dimensions": strconv.Itoa(s.dimensions),
		},
	}
}
