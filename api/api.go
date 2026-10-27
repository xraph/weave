// Package api provides Forge-style HTTP handlers for the Weave RAG engine.
package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/xraph/forge"

	"github.com/xraph/weave"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
)

// API wires all Forge-style HTTP handlers together for the Weave system.
type API struct {
	eng    *engine.Engine
	router forge.Router
}

// New creates an API from a Weave Engine.
func New(eng *engine.Engine, router forge.Router) *API {
	return &API{eng: eng, router: router}
}

// Handler returns the fully assembled http.Handler with all routes. It
// panics when a route fails to register, the way http.ServeMux does on a
// conflicting pattern, because a handler missing routes would answer 404
// where it should answer. Call RegisterRoutes to get the error instead.
func (a *API) Handler() http.Handler {
	if a.router == nil {
		a.router = forge.NewRouter()
	}
	if err := a.RegisterRoutes(a.router); err != nil {
		panic(err)
	}
	return a.router.Handler()
}

// RegisterRoutes registers all Weave API routes into the given Forge router
// with full OpenAPI metadata. It tries every route and returns an error
// naming each one that failed to register, so a router that refuses a
// route never leaves the API silently missing it.
func (a *API) RegisterRoutes(router forge.Router) error {
	return errors.Join(
		a.registerCollectionRoutes(router),
		a.registerDocumentRoutes(router),
		a.registerRetrievalRoutes(router),
	)
}

// named wraps a route registration error with the route's operation ID.
func named(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("weave: register route %s: %w", operation, err)
}

// requireTenant answers 403 for a request whose context has no tenant,
// before the request is bound or validated, so every route refuses it the
// same way whatever the body or path holds.
func requireTenant(next forge.Handler) forge.Handler {
	return func(c forge.Context) error {
		if weave.TenantFromContext(c.Context()) == "" {
			return mapStoreError(weave.ErrTenantRequired)
		}
		return next(c)
	}
}

// registerCollectionRoutes registers collection management routes.
func (a *API) registerCollectionRoutes(router forge.Router) error {
	g := router.Group("/v1", forge.WithGroupTags("collections"), forge.WithGroupMiddleware(requireTenant))

	return errors.Join(
		named("createCollection", g.POST("/collections", a.createCollection,
			forge.WithSummary("Create collection"),
			forge.WithDescription("Creates a new document collection with the specified embedding and chunking configuration."),
			forge.WithOperationID("createCollection"),
			forge.WithRequestSchema(CreateCollectionRequest{}),
			forge.WithCreatedResponse(&collection.Collection{}),
			forge.WithErrorResponses(),
		)),
		named("listCollections", g.GET("/collections", a.listCollections,
			forge.WithSummary("List collections"),
			forge.WithDescription("Returns collections with optional pagination."),
			forge.WithOperationID("listCollections"),
			forge.WithRequestSchema(ListCollectionsRequest{}),
			forge.WithResponseSchema(http.StatusOK, "Collection list", []*collection.Collection{}),
			forge.WithErrorResponses(),
		)),
		named("getCollection", g.GET("/collections/:collectionId", a.getCollection,
			forge.WithSummary("Get collection"),
			forge.WithDescription("Returns details of a specific collection."),
			forge.WithOperationID("getCollection"),
			forge.WithResponseSchema(http.StatusOK, "Collection details", &collection.Collection{}),
			forge.WithErrorResponses(),
		)),
		named("deleteCollection", g.DELETE("/collections/:collectionId", a.deleteCollection,
			forge.WithSummary("Delete collection"),
			forge.WithDescription("Deletes a collection and all its documents and chunks."),
			forge.WithOperationID("deleteCollection"),
			forge.WithNoContentResponse(),
			forge.WithErrorResponses(),
		)),
		named("collectionStats", g.GET("/collections/:collectionId/stats", a.collectionStats,
			forge.WithSummary("Collection statistics"),
			forge.WithDescription("Returns aggregate statistics for a collection."),
			forge.WithOperationID("collectionStats"),
			forge.WithResponseSchema(http.StatusOK, "Collection statistics", engine.CollectionStatsResult{}),
			forge.WithErrorResponses(),
		)),
		named("reindexCollection", g.POST("/collections/:collectionId/reindex", a.reindexCollection,
			forge.WithSummary("Reindex collection"),
			forge.WithDescription("Re-embeds and re-stores all chunks in the collection."),
			forge.WithOperationID("reindexCollection"),
			forge.WithNoContentResponse(),
			forge.WithErrorResponses(),
		)),
	)
}

// registerDocumentRoutes registers document management routes.
func (a *API) registerDocumentRoutes(router forge.Router) error {
	g := router.Group("/v1", forge.WithGroupTags("documents"), forge.WithGroupMiddleware(requireTenant))

	return errors.Join(
		named("ingestDocument", g.POST("/collections/:collectionId/documents", a.ingestDocument,
			forge.WithSummary("Ingest document"),
			forge.WithDescription("Ingests a single document into a collection: chunk, embed, and store."),
			forge.WithOperationID("ingestDocument"),
			forge.WithRequestSchema(IngestDocumentRequest{}),
			forge.WithCreatedResponse(&engine.IngestResult{}),
			forge.WithErrorResponses(),
		)),
		named("ingestBatch", g.POST("/collections/:collectionId/documents/batch", a.ingestBatch,
			forge.WithSummary("Ingest documents batch"),
			forge.WithDescription("Ingests multiple documents into a collection."),
			forge.WithOperationID("ingestBatch"),
			forge.WithRequestSchema(IngestBatchRequest{}),
			forge.WithCreatedResponse([]*engine.IngestResult{}),
			forge.WithErrorResponses(),
		)),
		named("listDocuments", g.GET("/collections/:collectionId/documents", a.listDocuments,
			forge.WithSummary("List documents"),
			forge.WithDescription("Returns documents in a collection with optional state filter."),
			forge.WithOperationID("listDocuments"),
			forge.WithRequestSchema(ListDocumentsRequest{}),
			forge.WithResponseSchema(http.StatusOK, "Document list", []*document.Document{}),
			forge.WithErrorResponses(),
		)),
		named("deleteDocumentsBySource", g.DELETE("/collections/:collectionId/documents", a.deleteDocumentsBySource,
			forge.WithSummary("Delete documents by source"),
			forge.WithDescription("Deletes every document of a source key in a collection, with their chunks and vectors. Nothing to delete is still 204; a missing source_key is 400."),
			forge.WithOperationID("deleteDocumentsBySource"),
			forge.WithRequestSchema(DeleteDocumentsBySourceRequest{}),
			forge.WithNoContentResponse(),
			forge.WithErrorResponses(),
		)),
		named("getDocument", g.GET("/documents/:documentId", a.getDocument,
			forge.WithSummary("Get document"),
			forge.WithDescription("Returns details of a specific document."),
			forge.WithOperationID("getDocument"),
			forge.WithResponseSchema(http.StatusOK, "Document details", &document.Document{}),
			forge.WithErrorResponses(),
		)),
		named("deleteDocument", g.DELETE("/documents/:documentId", a.deleteDocument,
			forge.WithSummary("Delete document"),
			forge.WithDescription("Deletes a document and its chunks from both metadata and vector stores."),
			forge.WithOperationID("deleteDocument"),
			forge.WithNoContentResponse(),
			forge.WithErrorResponses(),
		)),
	)
}

// registerRetrievalRoutes registers retrieval routes.
func (a *API) registerRetrievalRoutes(router forge.Router) error {
	g := router.Group("/v1", forge.WithGroupTags("retrieval"), forge.WithGroupMiddleware(requireTenant))

	return errors.Join(
		named("retrieve", g.POST("/collections/:collectionId/retrieve", a.retrieve,
			forge.WithSummary("Retrieve chunks"),
			forge.WithDescription("Performs semantic retrieval within a collection."),
			forge.WithOperationID("retrieve"),
			forge.WithRequestSchema(RetrieveRequest{}),
			forge.WithResponseSchema(http.StatusOK, "Retrieved chunks", []engine.ScoredChunk{}),
			forge.WithErrorResponses(),
		)),
		named("hybridSearch", g.POST("/search", a.hybridSearch,
			forge.WithSummary("Hybrid search"),
			forge.WithDescription("Performs retrieval across one or more collections."),
			forge.WithOperationID("hybridSearch"),
			forge.WithRequestSchema(HybridSearchRequest{}),
			forge.WithResponseSchema(http.StatusOK, "Search results", []engine.ScoredChunk{}),
			forge.WithErrorResponses(),
		)),
	)
}
