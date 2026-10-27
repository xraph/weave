package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/trace"

	log "github.com/xraph/go-utils/log"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/internal/telemetry"
	"github.com/xraph/weave/vectorstore"
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
// for a collection the tenant doesn't have, and content the source holds
// ready is answered Unchanged without embedding it again. New content is
// written beside the current version, whose vectors and chunks stay
// searchable until ReplaceSourceDocument swaps the two in one step.
//
// A document of this content that is still pending or processing is
// claimed: its ingest may have died with its request or its process, so
// this one chunks and embeds again with chunk IDs of its own and races it
// to ReplaceSourceDocument, which moves a document from processing to ready
// once. The attempt that loses sees ErrInvalidState, finds the document
// ready and answers Unchanged, and deletes only the vectors it wrote.
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

	// A ready copy of the content is the answer, and a failed one is purged
	// so this ingest can try again. A pending or processing one is claimed.
	doc, owner := sameContent(current, hash), false
	switch {
	case doc == nil:
	case doc.State == document.StateReady:
		return unchanged(doc), nil
	case doc.State == document.StateFailed:
		if err = e.purgeDocument(ctx, tenant, doc.ID); err != nil {
			return nil, fmt.Errorf("weave: purge failed document: %w", err)
		}
		doc = nil
	}

	if doc == nil {
		// The document starts processing, not pending. Nothing writes its
		// state again before ReplaceSourceDocument, whose guarded move from
		// processing to ready is the only step two attempts can race on, so
		// a late write can never knock a version another attempt has just
		// made ready back to processing.
		doc = newDocument(tenant, input, hash)
		doc.State = document.StateProcessing
		if createErr := e.store.CreateDocument(ctx, doc); createErr != nil {
			if !errors.Is(createErr, weave.ErrDuplicateDocument) {
				return nil, fmt.Errorf("weave: create document: %w", createErr)
			}
			// A concurrent ingest of this source and content won the insert,
			// or the content lives under another source or none.
			again, listErr := e.sourceDocuments(ctx, tenant, col.ID, input.SourceKey)
			if listErr != nil {
				return nil, listErr
			}
			doc = sameContent(again, hash)
			switch {
			case doc == nil || (doc.State != document.StatePending && doc.State != document.StateProcessing &&
				doc.State != document.StateReady):
				return nil, fmt.Errorf("weave: create document: %w", createErr)
			case doc.State == document.StateReady:
				return unchanged(doc), nil
			}
		} else {
			owner = true
		}
	}

	e.extensions.EmitIngestStarted(ctx, input.CollectionID, []*document.Document{doc})

	// A claimed document still pending (left by an ingest that isn't this
	// engine's keyed path) is moved to processing first. Every write below
	// hands the store a fresh copy: the memory store keeps the pointer it is
	// given, and other ingests of the source read it.
	if doc.State != document.StateProcessing {
		processing := *doc
		processing.State = document.StateProcessing
		if err = e.store.UpdateDocument(ctx, &processing); err != nil {
			return e.failSource(ctx, start, tenant, doc, owner, nil, fmt.Errorf("mark processing: %w", err))
		}
		doc = &processing
	}

	chunks, entries, err := e.embedChunks(ctx, span, col, doc, input)
	if err != nil {
		return e.failSource(ctx, start, tenant, doc, owner, nil, err)
	}

	// The vectors go in before the chunk rows. Until the swap below they
	// have no rows, so retrieval drops them (see hydrateHit).
	if err = e.upsertVectors(ctx, entries); err != nil {
		return e.failSource(ctx, start, tenant, doc, owner, entries, fmt.Errorf("upsert vectors: %w", err))
	}

	swap := *doc
	superseded, err := e.store.ReplaceSourceDocument(ctx, &swap, chunks)
	if err != nil {
		return e.failSource(ctx, start, tenant, doc, owner, entries, fmt.Errorf("replace source document: %w", err))
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

// failSource ends a keyed ingest attempt that started at start and failed
// with cause. It runs on a context the request's cancellation can't stop,
// so a client that gives up mid-embed still leaves the document failed and
// its vectors gone, and a retry of the same content embeds again.
//
// It reads the document back before it deletes anything. When the document
// is ready, the content is indexed and the answer is Unchanged, with the
// completion hook fired to match the start this attempt fired. If the ready
// rows are this attempt's own chunks (a swap that committed but reported an
// error), its vectors are the document's and stay; otherwise another
// attempt won, and the vectors this one wrote are deleted by their own IDs,
// since attempts on one document share its document ID. A document that
// isn't ready loses this attempt's vectors, and the owner (the attempt that
// created it) marks it failed through failIngest; a claimer leaves its
// state alone, so it never writes failed over a version another attempt is
// finishing.
func (e *Engine) failSource(ctx context.Context, start time.Time, tenant string, doc *document.Document, owner bool, entries []vectorstore.Entry, cause error) (*IngestResult, error) {
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()

	if now, err := e.getDocument(cleanup, tenant, doc.ID); err == nil && now.State == document.StateReady {
		if !e.attemptCommitted(cleanup, tenant, entries) {
			e.dropAttemptVectors(cleanup, entries)
		}
		e.extensions.EmitIngestCompleted(ctx, doc.CollectionID, 1, now.ChunkCount, time.Since(start))
		return unchanged(now), nil
	}
	e.dropAttemptVectors(cleanup, entries)
	if owner {
		return e.failIngest(ctx, doc, doc.CollectionID, cause)
	}
	e.extensions.EmitIngestFailed(ctx, doc.CollectionID, cause)
	return &IngestResult{DocumentID: doc.ID, State: doc.State}, fmt.Errorf("weave: ingest failed: %w", cause)
}

// attemptCommitted reports whether the chunk rows of this attempt's vectors
// exist, which means its swap went through. The swap writes every row or
// none, so the first entry answers for all of them.
func (e *Engine) attemptCommitted(ctx context.Context, tenant string, entries []vectorstore.Entry) bool {
	if len(entries) == 0 {
		return false
	}
	chunkID, err := id.ParseChunkID(entries[0].ID)
	if err != nil {
		return false
	}
	row, err := e.store.GetChunk(ctx, chunkID)
	return err == nil && row.TenantID == tenant
}

// sameContent returns the document in docs with hash, or nil. A collection
// holds one document per content hash, so there is at most one.
func sameContent(docs []*document.Document, hash string) *document.Document {
	for _, d := range docs {
		if d.ContentHash == hash {
			return d
		}
	}
	return nil
}

// unchanged is the answer for content the source already holds in doc,
// which is ready.
func unchanged(doc *document.Document) *IngestResult {
	return &IngestResult{DocumentID: doc.ID, ChunkCount: doc.ChunkCount, State: doc.State, Unchanged: true}
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

// dropAttemptVectors deletes the vectors in entries, which one ingest
// attempt wrote, so they don't wait for a purge. A failure is only logged:
// retrieval drops vectors without chunk rows, and the document's purge
// removes them later.
func (e *Engine) dropAttemptVectors(ctx context.Context, entries []vectorstore.Entry) {
	if len(entries) == 0 {
		return
	}
	ids := make([]string, len(entries))
	for i, en := range entries {
		ids[i] = en.ID
	}
	if err := e.vectorStore.Delete(ctx, ids); err != nil {
		e.logger.Warn("failed to delete vector entries for a failed ingest",
			log.Int("vectors", len(ids)),
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

// oneVersionPerSource keeps hits to one version of each source. Hydrate
// reads rows one at a time, so when a replace commits between two reads,
// an old version's hit can be read before the swap and the new version's
// after it, and both rows are found. For each source with hits from more
// than one document, only the hits of the version that is current now are
// kept (see currentVersion). Hits without a source_key pass through. The
// hits slice is reused.
func (e *Engine) oneVersionPerSource(ctx context.Context, tenant string, hits []ScoredChunk) ([]ScoredChunk, error) {
	chunks := make([]*chunk.Chunk, len(hits))
	for i := range hits {
		chunks[i] = hits[i].Chunk
	}
	keep, err := e.currentVersions(ctx, tenant, chunks)
	if err != nil {
		return nil, err
	}
	kept := hits[:0]
	for _, h := range hits {
		if keep(h.Chunk) {
			kept = append(kept, h)
		}
	}
	return kept, nil
}

// sourceRef names one source within one collection.
type sourceRef struct{ collection, key string }

// chunkSource reports the source and document a hit's chunk belongs to,
// from its row fields or its vector metadata. ok is false for a chunk
// without a source_key or a document ID.
func chunkSource(ch *chunk.Chunk) (ref sourceRef, docID string, ok bool) {
	if ch == nil || ch.Metadata["source_key"] == "" {
		return sourceRef{}, "", false
	}
	ref = sourceRef{collection: ch.CollectionID.String(), key: ch.Metadata["source_key"]}
	if ref.collection == "" {
		ref.collection = ch.Metadata["collection_id"]
	}
	docID = ch.DocumentID.String()
	if docID == "" {
		docID = ch.Metadata["document_id"]
	}
	return ref, docID, docID != ""
}

// currentVersions returns a filter over chunks that, for each source whose
// chunks come from more than one document, keeps only the chunks of the
// current version. The store is read only for such contested sources, and
// an engine without one keeps every chunk.
func (e *Engine) currentVersions(ctx context.Context, tenant string, chunks []*chunk.Chunk) (func(*chunk.Chunk) bool, error) {
	if e.store == nil {
		// Without a metadata store there is no version to read, and no row
		// either, so hits pass through as the vector side returned them.
		return func(*chunk.Chunk) bool { return true }, nil
	}
	versions := map[sourceRef][]string{}
	for _, ch := range chunks {
		ref, docID, ok := chunkSource(ch)
		if ok && !slices.Contains(versions[ref], docID) {
			versions[ref] = append(versions[ref], docID)
		}
	}
	winners := map[sourceRef]string{}
	for ref, docIDs := range versions {
		if len(docIDs) < 2 {
			continue
		}
		winner, err := e.currentVersion(ctx, tenant, docIDs)
		if err != nil {
			return nil, err
		}
		winners[ref] = winner
	}
	return func(ch *chunk.Chunk) bool {
		ref, docID, ok := chunkSource(ch)
		if !ok {
			return true
		}
		winner, contested := winners[ref]
		return !contested || docID == winner
	}, nil
}

// currentVersion picks one of docIDs, versions of one source: the one that
// is ready now (a source has at most one), else the newest by CreatedAt,
// else the highest ID, since TypeIDs sort by creation time. A version
// already purged is never ready.
func (e *Engine) currentVersion(ctx context.Context, tenant string, docIDs []string) (string, error) {
	var newest *document.Document
	for _, raw := range docIDs {
		docID, parseErr := id.ParseDocumentID(raw)
		if parseErr != nil {
			continue
		}
		doc, err := e.getDocument(ctx, tenant, docID)
		switch {
		case errors.Is(err, weave.ErrDocumentNotFound):
			continue
		case err != nil:
			return "", fmt.Errorf("weave: read version %s of a source: %w", raw, err)
		case doc.State == document.StateReady:
			return raw, nil
		}
		if newest == nil || doc.CreatedAt.After(newest.CreatedAt) ||
			doc.CreatedAt.Equal(newest.CreatedAt) && doc.ID.String() > newest.ID.String() {
			newest = doc
		}
	}
	if newest != nil {
		return newest.ID.String(), nil
	}
	return slices.Max(docIDs), nil
}
