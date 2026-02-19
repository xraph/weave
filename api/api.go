// Package api provides Forge-style HTTP handlers for the Weave RAG engine.
package api

import (
	"net/http"

	"github.com/xraph/forge"

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

// Handler returns the fully assembled http.Handler with all routes.
func (a *API) Handler() http.Handler {
	if a.router == nil {
		a.router = forge.NewRouter()
	}
	a.RegisterRoutes(a.router)
	return a.router.Handler()
}

// RegisterRoutes registers all Weave API routes into the given Forge router
// with full OpenAPI metadata.
func (a *API) RegisterRoutes(router forge.Router) {
	a.registerCollectionRoutes(router)
	a.registerDocumentRoutes(router)
	a.registerRetrievalRoutes(router)
}

// registerCollectionRoutes registers collection management routes.
func (a *API) registerCollectionRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("collections"))

	_ = g.POST("/collections", a.createCollection,
		forge.WithSummary("Create collection"),
		forge.WithDescription("Creates a new document collection with the specified embedding and chunking configuration."),
		forge.WithOperationID("createCollection"),
		forge.WithRequestSchema(CreateCollectionRequest{}),
		forge.WithCreatedResponse(&collection.Collection{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/collections", a.listCollections,
		forge.WithSummary("List collections"),
		forge.WithDescription("Returns collections with optional pagination."),
		forge.WithOperationID("listCollections"),
		forge.WithRequestSchema(ListCollectionsRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Collection list", []*collection.Collection{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/collections/:collectionId", a.getCollection,
		forge.WithSummary("Get collection"),
		forge.WithDescription("Returns details of a specific collection."),
		forge.WithOperationID("getCollection"),
		forge.WithResponseSchema(http.StatusOK, "Collection details", &collection.Collection{}),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/collections/:collectionId", a.deleteCollection,
		forge.WithSummary("Delete collection"),
		forge.WithDescription("Deletes a collection and all its documents and chunks."),
		forge.WithOperationID("deleteCollection"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/collections/:collectionId/stats", a.collectionStats,
		forge.WithSummary("Collection statistics"),
		forge.WithDescription("Returns aggregate statistics for a collection."),
		forge.WithOperationID("collectionStats"),
		forge.WithResponseSchema(http.StatusOK, "Collection statistics", engine.CollectionStatsResult{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/collections/:collectionId/reindex", a.reindexCollection,
		forge.WithSummary("Reindex collection"),
		forge.WithDescription("Re-embeds and re-stores all chunks in the collection."),
		forge.WithOperationID("reindexCollection"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)
}

// registerDocumentRoutes registers document management routes.
func (a *API) registerDocumentRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("documents"))

	_ = g.POST("/collections/:collectionId/documents", a.ingestDocument,
		forge.WithSummary("Ingest document"),
		forge.WithDescription("Ingests a single document into a collection: chunk, embed, and store."),
		forge.WithOperationID("ingestDocument"),
		forge.WithRequestSchema(IngestDocumentRequest{}),
		forge.WithCreatedResponse(&engine.IngestResult{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/collections/:collectionId/documents/batch", a.ingestBatch,
		forge.WithSummary("Ingest documents batch"),
		forge.WithDescription("Ingests multiple documents into a collection."),
		forge.WithOperationID("ingestBatch"),
		forge.WithRequestSchema(IngestBatchRequest{}),
		forge.WithCreatedResponse([]*engine.IngestResult{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/collections/:collectionId/documents", a.listDocuments,
		forge.WithSummary("List documents"),
		forge.WithDescription("Returns documents in a collection with optional state filter."),
		forge.WithOperationID("listDocuments"),
		forge.WithRequestSchema(ListDocumentsRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Document list", []*document.Document{}),
		forge.WithErrorResponses(),
	)

	_ = g.GET("/documents/:documentId", a.getDocument,
		forge.WithSummary("Get document"),
		forge.WithDescription("Returns details of a specific document."),
		forge.WithOperationID("getDocument"),
		forge.WithResponseSchema(http.StatusOK, "Document details", &document.Document{}),
		forge.WithErrorResponses(),
	)

	_ = g.DELETE("/documents/:documentId", a.deleteDocument,
		forge.WithSummary("Delete document"),
		forge.WithDescription("Deletes a document and its chunks from both metadata and vector stores."),
		forge.WithOperationID("deleteDocument"),
		forge.WithNoContentResponse(),
		forge.WithErrorResponses(),
	)
}

// registerRetrievalRoutes registers retrieval routes.
func (a *API) registerRetrievalRoutes(router forge.Router) {
	g := router.Group("/v1", forge.WithGroupTags("retrieval"))

	_ = g.POST("/collections/:collectionId/retrieve", a.retrieve,
		forge.WithSummary("Retrieve chunks"),
		forge.WithDescription("Performs semantic retrieval within a collection."),
		forge.WithOperationID("retrieve"),
		forge.WithRequestSchema(RetrieveRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Retrieved chunks", []engine.ScoredChunk{}),
		forge.WithErrorResponses(),
	)

	_ = g.POST("/search", a.hybridSearch,
		forge.WithSummary("Hybrid search"),
		forge.WithDescription("Performs retrieval across one or more collections."),
		forge.WithOperationID("hybridSearch"),
		forge.WithRequestSchema(HybridSearchRequest{}),
		forge.WithResponseSchema(http.StatusOK, "Search results", []engine.ScoredChunk{}),
		forge.WithErrorResponses(),
	)
}
