package contract

import (
	"context"
	"strings"

	"github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
)

// maxIngestBytes is the largest content the dashboard ingests. Content
// travels inside the contract envelope, and ingest runs synchronously
// inside the request.
const maxIngestBytes = 1 << 20

// spanCap is how many chunk spans documents.spans returns. Past it the
// answer says complete: false rather than silently truncating.
const spanCap = 5000

type documentsListInput struct {
	page
	CollectionID string  `json:"collection_id"`
	State        string  `json:"state"`
	Search       string  `json:"search"`
	Tenant       *string `json:"tenant"`
}

type span struct {
	ID          string `json:"id"`
	Index       int    `json:"index"`
	StartOffset int    `json:"start_offset"`
	EndOffset   int    `json:"end_offset"`
	TokenCount  int    `json:"token_count"`
}

// spansOutput lays a document's chunks out as byte ranges. Offsets are into
// the text after the loader extracted it and after trimming; the semantic
// and code chunkers approximate them.
type spansOutput struct {
	DocumentID    string `json:"document_id"`
	ContentLength int    `json:"content_length"`
	Spans         []span `json:"spans"`
	Total         int64  `json:"total"`
	Complete      bool   `json:"complete"`
}

type ingestInput struct {
	CollectionID string            `json:"collection_id"`
	Title        string            `json:"title"`
	Source       string            `json:"source"`
	SourceType   string            `json:"source_type"`
	Content      string            `json:"content"`
	Metadata     map[string]string `json:"metadata"`
}

// ingestOutput is the result of one synchronous ingest. A failed ingest is
// an answer: the document row exists in state failed, and Error is the
// reason Weave stored on it.
type ingestOutput struct {
	DocumentID string `json:"document_id"`
	State      string `json:"state"`
	ChunkCount int    `json:"chunk_count"`
	Error      string `json:"error,omitempty"`
}

func validState(s string) bool {
	switch document.State(s) {
	case "", document.StatePending, document.StateProcessing, document.StateReady, document.StateFailed:
		return true
	}
	return false
}

func documentsListHandler(deps Deps) func(context.Context, documentsListInput, contract.Principal) (listOutput[documentRow], error) {
	return func(ctx context.Context, in documentsListInput, _ contract.Principal) (listOutput[documentRow], error) {
		const intent = "documents.list"
		limit, offset, err := in.resolve()
		if err != nil {
			return listOutput[documentRow]{}, err
		}
		if !validState(in.State) {
			return listOutput[documentRow]{}, badRequest("state must be pending, processing, ready or failed")
		}
		colID, err := optionalCollectionID("collection_id", in.CollectionID)
		if err != nil {
			return listOutput[documentRow]{}, err
		}
		state := document.State(in.State)
		docs, err := deps.Engine.ListDocuments(ctx, &document.ListFilter{
			CollectionID: colID, State: state, Search: in.Search, Limit: limit, Offset: offset, SortDesc: true, Tenant: in.Tenant,
		})
		if err != nil {
			return listOutput[documentRow]{}, deps.mapError(intent, err)
		}
		total, err := deps.Engine.CountDocuments(ctx, &document.CountFilter{CollectionID: colID, State: state, Search: in.Search, Tenant: in.Tenant})
		if err != nil {
			return listOutput[documentRow]{}, deps.mapError(intent, err)
		}
		cache := newNameCache(deps.Engine)
		out := listOutput[documentRow]{Items: make([]documentRow, 0, len(docs)), Total: total, Limit: limit, Offset: offset}
		for _, d := range docs {
			row, err := deps.documentRow(ctx, cache, d)
			if err != nil {
				return listOutput[documentRow]{}, deps.mapError(intent, err)
			}
			out.Items = append(out.Items, row)
		}
		return out, nil
	}
}

func documentsGetHandler(deps Deps) func(context.Context, idInput, contract.Principal) (documentRow, error) {
	return func(ctx context.Context, in idInput, _ contract.Principal) (documentRow, error) {
		const intent = "documents.get"
		docID, err := parseDocumentID("id", in.ID)
		if err != nil {
			return documentRow{}, err
		}
		doc, err := deps.Engine.GetDocument(ctx, docID)
		if err != nil {
			return documentRow{}, deps.mapError(intent, err)
		}
		row, err := deps.documentRow(ctx, newNameCache(deps.Engine), doc)
		if err != nil {
			return documentRow{}, deps.mapError(intent, err)
		}
		return row, nil
	}
}

func documentsSpansHandler(deps Deps) func(context.Context, idInput, contract.Principal) (spansOutput, error) {
	return func(ctx context.Context, in idInput, _ contract.Principal) (spansOutput, error) {
		const intent = "documents.spans"
		docID, err := parseDocumentID("id", in.ID)
		if err != nil {
			return spansOutput{}, err
		}
		doc, err := deps.Engine.GetDocument(ctx, docID)
		if err != nil {
			return spansOutput{}, deps.mapError(intent, err)
		}
		chunks, err := deps.Engine.ListChunks(ctx, &chunk.ListFilter{DocumentID: docID, Limit: spanCap})
		if err != nil {
			return spansOutput{}, deps.mapError(intent, err)
		}
		total, err := deps.Engine.CountChunks(ctx, &chunk.CountFilter{DocumentID: docID})
		if err != nil {
			return spansOutput{}, deps.mapError(intent, err)
		}
		out := spansOutput{DocumentID: docID.String(), ContentLength: doc.ContentLength, Spans: make([]span, 0, len(chunks)), Total: total, Complete: total <= spanCap}
		for _, c := range chunks {
			out.Spans = append(out.Spans, span{ID: c.ID.String(), Index: c.Index, StartOffset: c.StartOffset, EndOffset: c.EndOffset, TokenCount: c.TokenCount})
		}
		return out, nil
	}
}

func documentsIngestHandler(deps Deps) func(context.Context, ingestInput, contract.Principal) (ingestOutput, error) {
	return func(ctx context.Context, in ingestInput, _ contract.Principal) (ingestOutput, error) {
		const intent = "documents.ingest"
		colID, err := parseCollectionID("collection_id", in.CollectionID)
		if err != nil {
			return ingestOutput{}, err
		}
		if strings.TrimSpace(in.Content) == "" {
			return ingestOutput{}, badRequest("content is empty")
		}
		if len(in.Content) > maxIngestBytes {
			return ingestOutput{}, badRequest("content is larger than 1 MiB; the dashboard ingests up to 1 MiB")
		}
		res, err := deps.Engine.Ingest(ctx, &engine.IngestInput{
			CollectionID: colID, Title: in.Title, Source: in.Source, SourceType: in.SourceType,
			Content: in.Content, Metadata: in.Metadata,
		})
		if err != nil && res != nil && res.State == document.StateFailed {
			out := ingestOutput{DocumentID: res.DocumentID.String(), State: string(res.State), Error: err.Error()}
			if doc, getErr := deps.Engine.GetDocument(ctx, res.DocumentID); getErr == nil && doc.Error != "" {
				out.Error = doc.Error
			}
			return out, nil
		}
		if err != nil {
			return ingestOutput{}, deps.mapError(intent, err)
		}
		return ingestOutput{DocumentID: res.DocumentID.String(), State: string(res.State), ChunkCount: res.ChunkCount}, nil
	}
}

func documentsDeleteHandler(deps Deps) func(context.Context, idInput, contract.Principal) (idOutput, error) {
	return func(ctx context.Context, in idInput, _ contract.Principal) (idOutput, error) {
		docID, err := parseDocumentID("id", in.ID)
		if err != nil {
			return idOutput{}, err
		}
		if err := deps.Engine.DeleteDocument(ctx, docID); err != nil {
			return idOutput{}, deps.mapError("documents.delete", err)
		}
		return idOutput{ID: docID.String()}, nil
	}
}
