package contract

import (
	"context"

	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

// tenantInput is the input of an intent whose only parameter is the tenant
// filter: absent means every tenant, present is an exact match.
type tenantInput struct {
	Tenant *string `json:"tenant"`
}

// stateCounts is how many documents sit in each state.
type stateCounts struct {
	Pending    int64 `json:"pending"`
	Processing int64 `json:"processing"`
	Ready      int64 `json:"ready"`
	Failed     int64 `json:"failed"`
}

func countStates(ctx context.Context, e *engine.Engine, colID id.CollectionID, tenant *string) (stateCounts, error) {
	var out stateCounts
	for _, s := range []struct {
		state document.State
		into  *int64
	}{
		{document.StatePending, &out.Pending},
		{document.StateProcessing, &out.Processing},
		{document.StateReady, &out.Ready},
		{document.StateFailed, &out.Failed},
	} {
		n, err := e.CountDocuments(ctx, &document.CountFilter{CollectionID: colID, State: s.state, Tenant: tenant})
		if err != nil {
			return stateCounts{}, err
		}
		*s.into = n
	}
	return out, nil
}

// documentRow is a document as the dashboard lists it: the stored row,
// its collection's name, and whether it looks stalled.
type documentRow struct {
	*document.Document
	CollectionName string `json:"collection_name"`
	// Stalled is true for a document still processing StalledAfter after
	// its last update. Weave has no heartbeat, so this is an age, not a
	// verdict.
	Stalled bool `json:"stalled"`
}

// nameCache resolves collection names once per request.
type nameCache struct {
	e     *engine.Engine
	names map[string]string
}

func newNameCache(e *engine.Engine) *nameCache {
	return &nameCache{e: e, names: map[string]string{}}
}

// name answers "" for a collection that no longer exists.
func (c *nameCache) name(ctx context.Context, colID id.CollectionID) string {
	key := colID.String()
	if n, ok := c.names[key]; ok {
		return n
	}
	n := ""
	if col, err := c.e.GetCollection(ctx, colID); err == nil {
		n = col.Name
	}
	c.names[key] = n
	return n
}

func (d Deps) documentRow(ctx context.Context, cache *nameCache, doc *document.Document) documentRow {
	cp := *doc
	cp.Metadata = emptyIfNil(cp.Metadata)
	return documentRow{
		Document:       &cp,
		CollectionName: cache.name(ctx, cp.CollectionID),
		Stalled:        cp.State == document.StateProcessing && d.now().Sub(cp.UpdatedAt) > StalledAfter,
	}
}
