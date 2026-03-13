package dashboard

import (
	"context"
	"strconv"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/dashboard/shared"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/store"
)

// PaginationMeta is an alias for shared.PaginationMeta.
type PaginationMeta = shared.PaginationMeta

// NewPaginationMeta is a convenience re-export.
var NewPaginationMeta = shared.NewPaginationMeta

// --- Helper Functions ---

func parseIntParam(params map[string]string, key string, defaultVal int) int {
	v, ok := params[key]
	if !ok || v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultVal
	}
	return n
}

func parseFloat64Param(params map[string]string, key string, defaultVal float64) float64 {
	v, ok := params[key]
	if !ok || v == "" {
		return defaultVal
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return defaultVal
	}
	return f
}

// --- Entity Counts ---

type entityCounts struct {
	Collections    int64
	Documents      int64
	DocsReady      int64
	DocsProcessing int64
	DocsFailed     int64
	DocsPending    int64
	Chunks         int64
}

//nolint:errcheck // Dashboard counts are best-effort; partial data is acceptable.
func fetchEntityCounts(ctx context.Context, s store.Store) entityCounts {
	var c entityCounts
	c.Collections, _ = s.CountCollections(ctx, &collection.CountFilter{})
	c.Documents, _ = s.CountDocuments(ctx, &document.CountFilter{})
	c.DocsReady, _ = s.CountDocuments(ctx, &document.CountFilter{State: document.StateReady})
	c.DocsProcessing, _ = s.CountDocuments(ctx, &document.CountFilter{State: document.StateProcessing})
	c.DocsFailed, _ = s.CountDocuments(ctx, &document.CountFilter{State: document.StateFailed})
	c.DocsPending, _ = s.CountDocuments(ctx, &document.CountFilter{State: document.StatePending})
	c.Chunks, _ = s.CountChunks(ctx, &chunk.CountFilter{})
	return c
}

// --- Paginated Fetch Functions ---

func fetchCollectionsPaginated(ctx context.Context, s store.Store, search string, limit, offset int) ([]*collection.Collection, int64, error) {
	filter := &collection.ListFilter{Search: search, Limit: limit, Offset: offset}
	items, err := s.ListCollections(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	total, _ := s.CountCollections(ctx, &collection.CountFilter{Search: search}) //nolint:errcheck // best-effort count
	return items, total, nil
}

func fetchDocumentsPaginated(ctx context.Context, s store.Store, colIDStr, state, search string, limit, offset int) ([]*document.Document, int64, error) {
	filter := &document.ListFilter{
		State:  document.State(state),
		Search: search,
		Limit:  limit,
		Offset: offset,
	}
	countFilter := &document.CountFilter{State: document.State(state)}
	if colIDStr != "" {
		colID, err := id.ParseCollectionID(colIDStr)
		if err == nil {
			filter.CollectionID = colID
			countFilter.CollectionID = colID
		}
	}
	items, err := s.ListDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	total, _ := s.CountDocuments(ctx, countFilter) //nolint:errcheck // best-effort count
	return items, total, nil
}

func fetchRecentDocuments(ctx context.Context, s store.Store, limit int) ([]*document.Document, error) {
	return s.ListDocuments(ctx, &document.ListFilter{Limit: limit})
}

// --- Pipeline Status ---

type PipelineStatus struct {
	HasLoader      bool
	HasChunker     bool
	HasEmbedder    bool
	HasVectorStore bool
	HasRetriever   bool
}

func fetchPipelineStatus(eng *engine.Engine) PipelineStatus {
	return PipelineStatus{
		HasLoader:      true,
		HasChunker:     true,
		HasEmbedder:    eng.Store() != nil,
		HasVectorStore: eng.Store() != nil,
		HasRetriever:   eng.Store() != nil,
	}
}

// --- Collection Name Map ---

func buildCollectionNameMap(ctx context.Context, s store.Store) map[string]string {
	cols, err := s.ListCollections(ctx, &collection.ListFilter{})
	if err != nil {
		return map[string]string{}
	}
	m := make(map[string]string, len(cols))
	for _, c := range cols {
		m[c.ID.String()] = c.Name
	}
	return m
}
