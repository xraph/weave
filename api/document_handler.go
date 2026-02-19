package api

import (
	"fmt"
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
)

func (a *API) ingestDocument(ctx forge.Context, req *IngestDocumentRequest) (*engine.IngestResult, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	if req.Content == "" {
		return nil, forge.BadRequest("content is required")
	}

	result, err := a.eng.Ingest(ctx.Context(), &engine.IngestInput{
		CollectionID: colID,
		Title:        req.Title,
		Source:       req.Source,
		SourceType:   req.SourceType,
		Content:      req.Content,
		Metadata:     req.Metadata,
	})
	if err != nil {
		return nil, fmt.Errorf("ingest document: %w", err)
	}

	return result, ctx.JSON(http.StatusCreated, result)
}

func (a *API) ingestBatch(ctx forge.Context, req *IngestBatchRequest) ([]*engine.IngestResult, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	inputs := make([]*engine.IngestInput, len(req.Documents))
	for i, doc := range req.Documents {
		if doc.Content == "" {
			return nil, forge.BadRequest(fmt.Sprintf("document %d: content is required", i))
		}
		inputs[i] = &engine.IngestInput{
			CollectionID: colID,
			Title:        doc.Title,
			Source:       doc.Source,
			SourceType:   doc.SourceType,
			Content:      doc.Content,
			Metadata:     doc.Metadata,
		}
	}

	results, err := a.eng.IngestBatch(ctx.Context(), inputs)
	if err != nil {
		return results, fmt.Errorf("ingest batch: %w", err)
	}

	return results, ctx.JSON(http.StatusCreated, results)
}

func (a *API) getDocument(ctx forge.Context, _ *GetDocumentRequest) (*document.Document, error) {
	docID, err := id.ParseDocumentID(ctx.Param("documentId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid document ID: %v", err))
	}

	doc, err := a.eng.GetDocument(ctx.Context(), docID)
	if err != nil {
		return nil, mapStoreError(err)
	}

	return doc, ctx.JSON(http.StatusOK, doc)
}

func (a *API) listDocuments(ctx forge.Context, req *ListDocumentsRequest) ([]*document.Document, error) {
	colID, err := id.ParseCollectionID(ctx.Param("collectionId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid collection ID: %v", err))
	}

	docs, err := a.eng.ListDocuments(ctx.Context(), &document.ListFilter{
		CollectionID: colID,
		State:        document.State(req.State),
		Limit:        defaultLimit(req.Limit),
		Offset:       req.Offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list documents: %w", err)
	}

	return docs, ctx.JSON(http.StatusOK, docs)
}

func (a *API) deleteDocument(ctx forge.Context, _ *DeleteDocumentRequest) (*struct{}, error) {
	docID, err := id.ParseDocumentID(ctx.Param("documentId"))
	if err != nil {
		return nil, forge.BadRequest(fmt.Sprintf("invalid document ID: %v", err))
	}

	if err := a.eng.DeleteDocument(ctx.Context(), docID); err != nil {
		return nil, mapStoreError(err)
	}

	return nil, ctx.NoContent(http.StatusNoContent)
}
