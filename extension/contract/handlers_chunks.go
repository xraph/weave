package contract

import (
	"context"
	"errors"

	"github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
)

type chunksListInput struct {
	page
	DocumentID   string  `json:"document_id"`
	CollectionID string  `json:"collection_id"`
	Tenant       *string `json:"tenant"`
}

// chunkDetail is one chunk with its neighbours in its document. An empty
// previous_id or next_id means there is none.
type chunkDetail struct {
	Chunk         *chunk.Chunk `json:"chunk"`
	DocumentTitle string       `json:"document_title"`
	PreviousID    string       `json:"previous_id"`
	NextID        string       `json:"next_id"`
}

func copyChunk(c *chunk.Chunk) *chunk.Chunk {
	cp := *c
	cp.Metadata = emptyIfNil(cp.Metadata)
	return &cp
}

func chunksListHandler(deps Deps) func(context.Context, chunksListInput, contract.Principal) (listOutput[*chunk.Chunk], error) {
	return func(ctx context.Context, in chunksListInput, _ contract.Principal) (listOutput[*chunk.Chunk], error) {
		const intent = "chunks.list"
		limit, offset, err := in.resolve()
		if err != nil {
			return listOutput[*chunk.Chunk]{}, err
		}
		docID, err := optionalDocumentID("document_id", in.DocumentID)
		if err != nil {
			return listOutput[*chunk.Chunk]{}, err
		}
		colID, err := optionalCollectionID("collection_id", in.CollectionID)
		if err != nil {
			return listOutput[*chunk.Chunk]{}, err
		}
		chunks, err := deps.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: docID, CollectionID: colID, Tenant: in.Tenant, Limit: limit, Offset: offset})
		if err != nil {
			return listOutput[*chunk.Chunk]{}, deps.mapError(intent, err)
		}
		total, err := deps.Engine.CountChunks(ctx, &chunk.CountFilter{DocumentID: docID, CollectionID: colID, Tenant: in.Tenant})
		if err != nil {
			return listOutput[*chunk.Chunk]{}, deps.mapError(intent, err)
		}
		out := listOutput[*chunk.Chunk]{Items: make([]*chunk.Chunk, 0, len(chunks)), Total: total, Limit: limit, Offset: offset}
		for _, c := range chunks {
			out.Items = append(out.Items, copyChunk(c))
		}
		return out, nil
	}
}

func chunksGetHandler(deps Deps) func(context.Context, idInput, contract.Principal) (chunkDetail, error) {
	return func(ctx context.Context, in idInput, _ contract.Principal) (chunkDetail, error) {
		const intent = "chunks.get"
		chunkID, err := parseChunkID("id", in.ID)
		if err != nil {
			return chunkDetail{}, err
		}
		c, err := deps.Engine.GetChunk(ctx, chunkID)
		if err != nil {
			return chunkDetail{}, deps.mapError(intent, err)
		}
		out := chunkDetail{Chunk: copyChunk(c)}
		doc, err := deps.Engine.GetDocument(ctx, c.DocumentID)
		switch {
		case err == nil:
			out.DocumentTitle = doc.Title
		case errors.Is(err, weave.ErrDocumentNotFound):
			// An orphaned chunk: its document row is gone but the chunk exists,
			// and chunks.list shows it, so the detail answers it with an empty title.
		default:
			return chunkDetail{}, deps.mapError(intent, err)
		}
		// Relies on a document's chunks being indexed contiguously from 0, which
		// the chunkers guarantee.
		neighbours, err := deps.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: c.DocumentID, Offset: max(c.Index-1, 0), Limit: 3})
		if err != nil {
			return chunkDetail{}, deps.mapError(intent, err)
		}
		for _, n := range neighbours {
			switch n.Index {
			case c.Index - 1:
				out.PreviousID = n.ID.String()
			case c.Index + 1:
				out.NextID = n.ID.String()
			}
		}
		return out, nil
	}
}
