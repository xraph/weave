package tenancy

import (
	"fmt"

	"github.com/xraph/weave"
	"github.com/xraph/weave/vectorstore"
)

// The checks below are the tenant rules every built-in vector store runs
// before it touches data. A vector store has no context tenant of its own:
// the engine labels each entry with metadata["tenant_id"] and names the
// tenant in SearchOptions.TenantKey.

// VectorEntries refuses a batch in which any entry has no tenant_id, before
// any of it is written.
func VectorEntries(entries []vectorstore.Entry) error {
	for _, e := range entries {
		if e.Metadata["tenant_id"] == "" {
			return fmt.Errorf("weave: upsert vector %q: %w", e.ID, weave.ErrTenantRequired)
		}
	}
	return nil
}

// VectorSearch refuses a search that names no tenant. There is no search
// across every tenant.
func VectorSearch(opts *vectorstore.SearchOptions) error {
	if opts == nil || opts.TenantKey == "" {
		return fmt.Errorf("weave: vector search: %w", weave.ErrTenantRequired)
	}
	return nil
}

// VectorDeleteFilter refuses an empty delete filter, which would match every
// entry of every tenant.
func VectorDeleteFilter(filter map[string]string) error {
	if len(filter) == 0 {
		return fmt.Errorf("%w: delete by metadata needs a filter", weave.ErrInvalidArgument)
	}
	return nil
}
