package contract

import (
	"context"
	"errors"
	"strings"

	"github.com/xraph/forge"
	"github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/id"
)

// mapError turns a Weave error into a *contract.Error the client can branch
// on. Anything unrecognised becomes CodeInternal with a generic message: a
// store error can carry a DSN or a host, so its text never reaches the
// client. Handlers call it through Deps.mapError, which logs that case.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var ce *contract.Error
	switch {
	case errors.As(err, &ce):
		return ce
	case errors.Is(err, weave.ErrCollectionNotFound):
		return notFound("collection not found")
	case errors.Is(err, weave.ErrDocumentNotFound):
		return notFound("document not found")
	case errors.Is(err, weave.ErrChunkNotFound):
		return notFound("chunk not found")
	case errors.Is(err, weave.ErrDuplicateDocument):
		return conflict("this collection already has a document with exactly the same content")
	case errors.Is(err, weave.ErrCollectionAlreadyExists):
		return conflict("a collection with this name already exists")
	case errors.Is(err, weave.ErrEmptyContent):
		return badRequest("content is empty")
	case errors.Is(err, weave.ErrInvalidArgument):
		reason := strings.TrimPrefix(err.Error(), weave.ErrInvalidArgument.Error()+": ")
		if reason == err.Error() {
			reason = "invalid argument"
		}
		return badRequest(reason)
	case errors.Is(err, weave.ErrNoStore):
		return unavailable("Weave has no metadata store configured")
	case errors.Is(err, weave.ErrNoEmbedder):
		return unavailable("Weave has no embedder configured, so it cannot ingest or search")
	case errors.Is(err, weave.ErrNoVectorStore):
		return unavailable("Weave has no vector store configured, so it cannot ingest or search")
	case errors.Is(err, weave.ErrNoChunker):
		return unavailable("Weave has no chunker configured, so it cannot ingest")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// A page that navigates away cancels its requests. That is
		// routine, so it is retryable and never logged as an error.
		return &contract.Error{Code: contract.CodeUnavailable, Message: "the request was cancelled or timed out", Retryable: true}
	default:
		return &contract.Error{Code: contract.CodeInternal, Message: "an internal error occurred"}
	}
}

// mapError maps err and, for CodeInternal with a logger set, logs the real
// error with the intent that hit it. That is the one case an operator
// cannot diagnose from what the client sees.
func (d Deps) mapError(intent string, err error) error {
	mapped := mapError(err)
	if d.Logger == nil || mapped == nil {
		return mapped
	}
	var ce *contract.Error
	if errors.As(mapped, &ce) && ce.Code == contract.CodeInternal {
		d.Logger.Error("weave/contract: internal error answering intent",
			forge.F("intent", intent),
			forge.F("error", err),
		)
	}
	return mapped
}

func badRequest(msg string) error {
	return &contract.Error{Code: contract.CodeBadRequest, Message: msg}
}
func notFound(msg string) error { return &contract.Error{Code: contract.CodeNotFound, Message: msg} }
func conflict(msg string) error { return &contract.Error{Code: contract.CodeConflict, Message: msg} }
func unavailable(msg string) error {
	return &contract.Error{Code: contract.CodeUnavailable, Message: msg}
}

func parseCollectionID(field, s string) (id.CollectionID, error) {
	if strings.TrimSpace(s) == "" {
		return id.Nil, badRequest(field + " is required")
	}
	v, err := id.ParseCollectionID(s)
	if err != nil {
		return id.Nil, badRequest(field + " is not a collection ID")
	}
	return v, nil
}

func parseDocumentID(field, s string) (id.DocumentID, error) {
	if strings.TrimSpace(s) == "" {
		return id.Nil, badRequest(field + " is required")
	}
	v, err := id.ParseDocumentID(s)
	if err != nil {
		return id.Nil, badRequest(field + " is not a document ID")
	}
	return v, nil
}

func parseChunkID(field, s string) (id.ChunkID, error) {
	if strings.TrimSpace(s) == "" {
		return id.Nil, badRequest(field + " is required")
	}
	v, err := id.ParseChunkID(s)
	if err != nil {
		return id.Nil, badRequest(field + " is not a chunk ID")
	}
	return v, nil
}

// optionalCollectionID treats an empty value as "no filter".
func optionalCollectionID(field, s string) (id.CollectionID, error) {
	if s == "" {
		return id.Nil, nil
	}
	return parseCollectionID(field, s)
}

// optionalDocumentID treats an empty value as "no filter".
func optionalDocumentID(field, s string) (id.DocumentID, error) {
	if s == "" {
		return id.Nil, nil
	}
	return parseDocumentID(field, s)
}
