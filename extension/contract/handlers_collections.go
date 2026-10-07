package contract

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
)

type idInput struct {
	ID string `json:"id"`
}

type idOutput struct {
	ID string `json:"id"`
}

// collectionRow is a collection with LIVE document and chunk counts. The
// stored count columns are never updated, so these fields shadow them.
type collectionRow struct {
	*collection.Collection
	DocumentCount int64 `json:"document_count"`
	ChunkCount    int64 `json:"chunk_count"`
}

type collectionDetail struct {
	collectionRow
	DocumentsByState stateCounts `json:"documents_by_state"`
	Stalled          int64       `json:"stalled"`
}

type collectionsListInput struct {
	page
	Tenant *string `json:"tenant"`
	Search string  `json:"search"`
}

type collectionCreateInput struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	ChunkSize    int               `json:"chunk_size"`
	ChunkOverlap int               `json:"chunk_overlap"`
	Metadata     map[string]string `json:"metadata"`
}

type collectionUpdateInput struct {
	ID          string             `json:"id"`
	Name        *string            `json:"name"`
	Description *string            `json:"description"`
	Metadata    *map[string]string `json:"metadata"`
}

type reindexOutput struct {
	ID                 string  `json:"id"`
	ReindexedDocuments int64   `json:"reindexed_documents"`
	ElapsedMillis      float64 `json:"elapsed_ms"`
}

func (d Deps) collectionRow(ctx context.Context, col *collection.Collection) (collectionRow, error) {
	cp := *col
	cp.Metadata = emptyIfNil(cp.Metadata)
	docs, err := d.Engine.CountDocuments(ctx, &document.CountFilter{CollectionID: cp.ID})
	if err != nil {
		return collectionRow{}, err
	}
	chunks, err := d.Engine.CountChunks(ctx, &chunk.CountFilter{CollectionID: cp.ID})
	if err != nil {
		return collectionRow{}, err
	}
	return collectionRow{Collection: &cp, DocumentCount: docs, ChunkCount: chunks}, nil
}

func collectionsListHandler(deps Deps) func(context.Context, collectionsListInput, contract.Principal) (listOutput[collectionRow], error) {
	return func(ctx context.Context, in collectionsListInput, _ contract.Principal) (listOutput[collectionRow], error) {
		const intent = "collections.list"
		limit, offset, err := in.resolve()
		if err != nil {
			return listOutput[collectionRow]{}, err
		}
		cols, err := deps.Engine.ListCollections(ctx, &collection.ListFilter{Search: in.Search, Limit: limit, Offset: offset, SortDesc: true, Tenant: in.Tenant})
		if err != nil {
			return listOutput[collectionRow]{}, deps.mapError(intent, err)
		}
		total, err := deps.Engine.CountCollections(ctx, &collection.CountFilter{Search: in.Search, Tenant: in.Tenant})
		if err != nil {
			return listOutput[collectionRow]{}, deps.mapError(intent, err)
		}
		out := listOutput[collectionRow]{Items: make([]collectionRow, 0, len(cols)), Total: total, Limit: limit, Offset: offset}
		for _, c := range cols {
			row, err := deps.collectionRow(ctx, c)
			if err != nil {
				return listOutput[collectionRow]{}, deps.mapError(intent, err)
			}
			out.Items = append(out.Items, row)
		}
		return out, nil
	}
}

func collectionsGetHandler(deps Deps) func(context.Context, idInput, contract.Principal) (collectionDetail, error) {
	return func(ctx context.Context, in idInput, _ contract.Principal) (collectionDetail, error) {
		const intent = "collections.get"
		colID, err := parseCollectionID("id", in.ID)
		if err != nil {
			return collectionDetail{}, err
		}
		col, err := deps.Engine.GetCollection(ctx, colID)
		if err != nil {
			return collectionDetail{}, deps.mapError(intent, err)
		}
		row, err := deps.collectionRow(ctx, col)
		if err != nil {
			return collectionDetail{}, deps.mapError(intent, err)
		}
		states, err := countStates(ctx, deps.Engine, colID, nil)
		if err != nil {
			return collectionDetail{}, deps.mapError(intent, err)
		}
		stalled, err := deps.Engine.CountDocuments(ctx, &document.CountFilter{
			CollectionID: colID, State: document.StateProcessing, UpdatedBefore: deps.now().Add(-StalledAfter),
		})
		if err != nil {
			return collectionDetail{}, deps.mapError(intent, err)
		}
		return collectionDetail{collectionRow: row, DocumentsByState: states, Stalled: stalled}, nil
	}
}

// collectionsCreateHandler records the embedder's real dimensions. The
// model and strategy it records are the engine's defaults; Weave records
// them and never reads them back, which the form says.
func collectionsCreateHandler(deps Deps) func(context.Context, collectionCreateInput, contract.Principal) (collectionRow, error) {
	return func(ctx context.Context, in collectionCreateInput, _ contract.Principal) (collectionRow, error) {
		const intent = "collections.create"
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return collectionRow{}, badRequest("name is required")
		}
		if in.ChunkSize < 0 || in.ChunkOverlap < 0 {
			return collectionRow{}, badRequest("chunk size and overlap cannot be negative")
		}
		cfg := deps.Engine.Config()
		size, overlap := in.ChunkSize, in.ChunkOverlap
		sizeFrom, overlapFrom := "", ""
		if size == 0 {
			size, sizeFrom = cfg.DefaultChunkSize, " (the default)"
		}
		if overlap == 0 {
			// The engine treats 0 as "use the default", so the check
			// must too.
			overlap, overlapFrom = cfg.DefaultChunkOverlap, " (the default)"
		}
		if overlap >= size {
			// Name the values the engine would use, and say which came
			// from the defaults: an overlap you never typed is otherwise
			// a mystery.
			return collectionRow{}, badRequest(fmt.Sprintf("chunk overlap %d%s must be smaller than chunk size %d%s", overlap, overlapFrom, size, sizeFrom))
		}
		col := &collection.Collection{
			Name: name, Description: in.Description,
			ChunkSize: in.ChunkSize, ChunkOverlap: in.ChunkOverlap,
			Metadata: emptyIfNil(in.Metadata),
		}
		if emb := deps.Engine.Components().Embedder; emb.Configured {
			col.EmbeddingDims = emb.Dimensions
		}
		if err := deps.Engine.CreateCollection(ctx, col); err != nil {
			return collectionRow{}, deps.mapError(intent, err)
		}
		row, err := deps.collectionRow(ctx, col)
		if err != nil {
			return collectionRow{}, deps.mapError(intent, err)
		}
		return row, nil
	}
}

func collectionsUpdateHandler(deps Deps) func(context.Context, collectionUpdateInput, contract.Principal) (collectionRow, error) {
	return func(ctx context.Context, in collectionUpdateInput, _ contract.Principal) (collectionRow, error) {
		const intent = "collections.update"
		colID, err := parseCollectionID("id", in.ID)
		if err != nil {
			return collectionRow{}, err
		}
		col, err := deps.Engine.UpdateCollection(ctx, colID, engine.CollectionUpdate{Name: in.Name, Description: in.Description, Metadata: in.Metadata})
		if err != nil {
			return collectionRow{}, deps.mapError(intent, err)
		}
		row, err := deps.collectionRow(ctx, col)
		if err != nil {
			return collectionRow{}, deps.mapError(intent, err)
		}
		return row, nil
	}
}

func collectionsDeleteHandler(deps Deps) func(context.Context, idInput, contract.Principal) (idOutput, error) {
	return func(ctx context.Context, in idInput, _ contract.Principal) (idOutput, error) {
		colID, err := parseCollectionID("id", in.ID)
		if err != nil {
			return idOutput{}, err
		}
		if err := deps.Engine.DeleteCollection(ctx, colID); err != nil {
			return idOutput{}, deps.mapError("collections.delete", err)
		}
		return idOutput{ID: colID.String()}, nil
	}
}

// collectionsReindexHandler re-embeds the collection's ready documents. It
// checks the collection exists first: the engine's reindex would delete
// nothing and report success for an ID that does not exist.
//
// The checks run on the request context. The reindex itself does not: it
// deletes every vector in the collection before re-embedding, so a closed
// tab or a proxy timeout partway would leave the collection partly indexed.
// No server-side timeout either, for the same reason.
func collectionsReindexHandler(deps Deps) func(context.Context, idInput, contract.Principal) (reindexOutput, error) {
	return func(ctx context.Context, in idInput, _ contract.Principal) (reindexOutput, error) {
		const intent = "collections.reindex"
		colID, err := parseCollectionID("id", in.ID)
		if err != nil {
			return reindexOutput{}, err
		}
		if _, err = deps.Engine.GetCollection(ctx, colID); err != nil {
			return reindexOutput{}, deps.mapError(intent, err)
		}
		ready, err := deps.Engine.CountDocuments(ctx, &document.CountFilter{CollectionID: colID, State: document.StateReady})
		if err != nil {
			return reindexOutput{}, deps.mapError(intent, err)
		}
		start := time.Now()
		if err := deps.Engine.ReindexCollection(context.WithoutCancel(ctx), colID); err != nil {
			return reindexOutput{}, deps.mapError(intent, err)
		}
		return reindexOutput{ID: colID.String(), ReindexedDocuments: ready, ElapsedMillis: float64(time.Since(start).Microseconds()) / 1000}, nil
	}
}
