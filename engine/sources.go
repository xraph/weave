package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	log "github.com/xraph/go-utils/log"

	"github.com/xraph/weave"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/telemetry"
)

// maxSourceKeyBytes is the longest source key Ingest and DeleteBySource
// accept.
const maxSourceKeyBytes = 1024

// checkSourceKey refuses a source key that is empty, all white space, or
// longer than maxSourceKeyBytes, with ErrInvalidArgument.
func checkSourceKey(key string) error {
	switch {
	case strings.TrimSpace(key) == "":
		return fmt.Errorf("%w: source key is empty", weave.ErrInvalidArgument)
	case len(key) > maxSourceKeyBytes:
		return fmt.Errorf("%w: source key is longer than %d bytes", weave.ErrInvalidArgument, maxSourceKeyBytes)
	}
	return nil
}

// ingestSource is Ingest's body for an input with a SourceKey, run under
// span for tenant. The collection is checked first, so nothing is embedded
// for a collection the tenant doesn't have, and content the source already
// holds is answered Unchanged without embedding it again. New content is
// written beside the current version, whose vectors and chunks stay
// searchable until ReplaceSourceDocument swaps the two in one step.
func (e *Engine) ingestSource(ctx context.Context, span trace.Span, tenant string, input *IngestInput) (*IngestResult, error) {
	start := time.Now()
	col, err := e.getCollection(ctx, tenant, input.CollectionID)
	if err != nil {
		return nil, err
	}
	hash := contentHash(input.Content)

	// Finish any replace that stopped before it purged what it superseded.
	docs, err := e.sourceDocuments(ctx, tenant, col.ID, input.SourceKey)
	if err != nil {
		return nil, err
	}
	current := docs[:0:0]
	for _, d := range docs {
		if d.State != document.StateSuperseded {
			current = append(current, d)
			continue
		}
		if err = e.purgeDocument(ctx, tenant, d.ID); err != nil {
			return nil, fmt.Errorf("weave: purge superseded document: %w", err)
		}
	}

	// The same content again is answered from what is there. A failed copy
	// of it is purged so this ingest can try again.
	if res := unchangedResult(current, hash); res != nil {
		return res, nil
	}
	for _, d := range current {
		if d.ContentHash != hash || d.State != document.StateFailed {
			continue
		}
		if err = e.purgeDocument(ctx, tenant, d.ID); err != nil {
			return nil, fmt.Errorf("weave: purge failed document: %w", err)
		}
	}

	doc := newDocument(tenant, input, hash)
	if createErr := e.store.CreateDocument(ctx, doc); createErr != nil {
		if errors.Is(createErr, weave.ErrDuplicateDocument) {
			// A concurrent ingest of this source and content won the insert,
			// or the content lives under another source or none.
			again, listErr := e.sourceDocuments(ctx, tenant, col.ID, input.SourceKey)
			if listErr != nil {
				return nil, listErr
			}
			if res := unchangedResult(again, hash); res != nil {
				return res, nil
			}
		}
		return nil, fmt.Errorf("weave: create document: %w", createErr)
	}

	e.extensions.EmitIngestStarted(ctx, input.CollectionID, []*document.Document{doc})

	// Every write below hands the store a fresh copy: the memory store keeps
	// the pointer it is given, and other ingests of the source read it.
	processing := *doc
	processing.State = document.StateProcessing
	if err = e.store.UpdateDocument(ctx, &processing); err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("mark processing: %w", err))
	}
	doc = &processing

	chunks, entries, err := e.embedChunks(ctx, span, col, doc, input)
	if err != nil {
		return e.failIngest(ctx, doc, input.CollectionID, err)
	}

	// The vectors go in before the chunk rows. Until the swap below they
	// have no rows, so retrieval drops them (see hydrateHit).
	if err = e.upsertVectors(ctx, entries); err != nil {
		e.dropDocumentVectors(ctx, tenant, doc.ID)
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("upsert vectors: %w", err))
	}

	swap := *doc
	superseded, err := e.store.ReplaceSourceDocument(ctx, &swap, chunks)
	if err != nil {
		e.dropDocumentVectors(ctx, tenant, doc.ID)
		return e.failIngest(ctx, doc, input.CollectionID, fmt.Errorf("replace source document: %w", err))
	}

	// The superseded versions have no chunk rows left, so their vectors are
	// already dropped from retrieval. A purge that fails here is finished by
	// the next ingest of the source.
	for _, old := range superseded {
		if purgeErr := e.purgeDocument(ctx, tenant, old); purgeErr != nil {
			e.logger.Warn("failed to purge a superseded document",
				log.String("document_id", old.String()),
				log.String("error_type", telemetry.ErrorType(purgeErr)),
			)
		}
	}

	e.extensions.EmitIngestCompleted(ctx, input.CollectionID, 1, len(chunks), time.Since(start))
	return &IngestResult{
		DocumentID: doc.ID,
		ChunkCount: len(chunks),
		State:      document.StateReady,
		Replaced:   superseded,
	}, nil
}

// unchangedResult is the answer for content the source already holds: a
// ready document with hash, or one still pending or processing (the same
// content is already in flight). It is nil when docs has neither.
func unchangedResult(docs []*document.Document, hash string) *IngestResult {
	for _, d := range docs {
		if d.ContentHash != hash {
			continue
		}
		switch d.State {
		case document.StateReady:
			return &IngestResult{DocumentID: d.ID, ChunkCount: d.ChunkCount, State: d.State, Unchanged: true}
		case document.StatePending, document.StateProcessing:
			return &IngestResult{DocumentID: d.ID, State: d.State, Unchanged: true}
		}
	}
	return nil
}

// sourceDocuments lists every document of sourceKey in colID within tenant,
// in any state.
func (e *Engine) sourceDocuments(ctx context.Context, tenant string, colID id.CollectionID, sourceKey string) ([]*document.Document, error) {
	docs, err := e.store.ListDocuments(ctx, &document.ListFilter{CollectionID: colID, SourceKey: sourceKey})
	if err != nil {
		return nil, fmt.Errorf("weave: list documents of source: %w", err)
	}
	return owned(docs, tenant, documentTenant), nil
}

// purgeDocument removes a document's vectors, chunks and row within tenant.
// A row already gone counts as purged, because another ingest of the same
// source may be finishing the same purge.
func (e *Engine) purgeDocument(ctx context.Context, tenant string, docID id.DocumentID) error {
	if e.vectorStore != nil {
		if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
			"document_id": docID.String(),
			"tenant_id":   tenant,
		}); err != nil {
			return fmt.Errorf("weave: delete vectors: %w", err)
		}
	}
	if err := e.store.DeleteChunksByDocument(ctx, docID); err != nil {
		return fmt.Errorf("weave: delete chunks: %w", err)
	}
	if err := e.store.DeleteDocument(ctx, docID); err != nil && !errors.Is(err, weave.ErrDocumentNotFound) {
		return fmt.Errorf("weave: delete document: %w", err)
	}
	return nil
}

// dropDocumentVectors deletes the vectors a failed keyed ingest wrote, so
// they don't wait for the failed document to be purged. A failure is only
// logged: retrieval already drops them, and the purge removes them later.
func (e *Engine) dropDocumentVectors(ctx context.Context, tenant string, docID id.DocumentID) {
	if err := e.vectorStore.DeleteByMetadata(ctx, map[string]string{
		"document_id": docID.String(),
		"tenant_id":   tenant,
	}); err != nil {
		e.logger.Warn("failed to delete vector entries for a failed ingest",
			log.String("document_id", docID.String()),
			log.String("error_type", telemetry.ErrorType(err)),
		)
	}
}

// DeleteBySource removes every document of sourceKey in the collection, with
// their chunks and vectors. Nothing to delete is not an error.
//
// It runs within the context tenant: another tenant's collection is
// ErrCollectionNotFound, and a source key that is empty, blank or longer
// than 1024 bytes is ErrInvalidArgument, both before anything is deleted.
// The source's vectors go first, filtered by collection, source and tenant,
// then each document's vectors, chunks and row. A version still being
// ingested when the documents are listed loses its row too, so that ingest
// fails at the swap instead of finishing; one that starts after the list
// is a new version and is kept.
func (e *Engine) DeleteBySource(ctx context.Context, colID id.CollectionID, sourceKey string) error {
	if e.store == nil {
		return weave.ErrNoStore
	}
	ctx, tenant, err := e.tenant(ctx)
	if err != nil {
		return err
	}
	if keyErr := checkSourceKey(sourceKey); keyErr != nil {
		return keyErr
	}
	if _, err = e.getCollection(ctx, tenant, colID); err != nil {
		return err
	}

	if e.vectorStore != nil {
		if err = e.vectorStore.DeleteByMetadata(ctx, map[string]string{
			"collection_id": colID.String(),
			"source_key":    sourceKey,
			"tenant_id":     tenant,
		}); err != nil {
			return fmt.Errorf("weave: delete vectors of source: %w", err)
		}
	}

	docs, err := e.sourceDocuments(ctx, tenant, colID, sourceKey)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if purgeErr := e.purgeDocument(ctx, tenant, doc.ID); purgeErr != nil {
			return purgeErr
		}
		e.extensions.EmitDocumentDeleted(ctx, doc.ID)
	}
	return nil
}
