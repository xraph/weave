package api

import (
	"github.com/xraph/weave/id"
)

// ──────────────────────────────────────────────────
// Collection requests
// ──────────────────────────────────────────────────

// CreateCollectionRequest is the request body for creating a collection.
type CreateCollectionRequest struct {
	Name           string            `json:"name" description:"Collection name"`
	Description    string            `json:"description,omitempty" description:"Human-readable description"`
	EmbeddingModel string            `json:"embedding_model,omitempty" description:"Embedding model name"`
	EmbeddingDims  int               `json:"embedding_dims,omitempty" description:"Embedding vector dimensions"`
	ChunkStrategy  string            `json:"chunk_strategy,omitempty" description:"Chunking strategy (recursive, fixed, etc.)"`
	ChunkSize      int               `json:"chunk_size,omitempty" description:"Target chunk size in tokens"`
	ChunkOverlap   int               `json:"chunk_overlap,omitempty" description:"Overlap between chunks in tokens"`
	Metadata       map[string]string `json:"metadata,omitempty" description:"Custom metadata"`
}

// GetCollectionRequest is the request for getting a collection by ID.
type GetCollectionRequest struct {
	CollectionID string `path:"collectionId" description:"Collection ID"`
}

// ListCollectionsRequest is the request for listing collections.
type ListCollectionsRequest struct {
	Limit  int `query:"limit" description:"Maximum number of results (default: 50)"`
	Offset int `query:"offset" description:"Number of results to skip"`
}

// DeleteCollectionRequest is the request for deleting a collection.
type DeleteCollectionRequest struct {
	CollectionID string `path:"collectionId" description:"Collection ID"`
}

// CollectionStatsRequest is the request for getting collection statistics.
type CollectionStatsRequest struct {
	CollectionID string `path:"collectionId" description:"Collection ID"`
}

// ReindexCollectionRequest is the request for reindexing a collection.
type ReindexCollectionRequest struct {
	CollectionID string `path:"collectionId" description:"Collection ID"`
}

// ──────────────────────────────────────────────────
// Document requests
// ──────────────────────────────────────────────────

// IngestDocumentRequest is the request body for ingesting a document.
type IngestDocumentRequest struct {
	CollectionID string            `path:"collectionId" description:"Collection ID"`
	Title        string            `json:"title,omitempty" description:"Document title"`
	Source       string            `json:"source,omitempty" description:"Source identifier (URL, path, etc.)"`
	SourceType   string            `json:"source_type,omitempty" description:"MIME type or format hint"`
	Content      string            `json:"content" description:"Document text content"`
	Metadata     map[string]string `json:"metadata,omitempty" description:"Custom metadata"`
}

// IngestBatchRequest is the request body for batch document ingestion.
type IngestBatchRequest struct {
	CollectionID string `path:"collectionId" description:"Collection ID"`
	Documents    []struct {
		Title      string            `json:"title,omitempty" description:"Document title"`
		Source     string            `json:"source,omitempty" description:"Source identifier"`
		SourceType string            `json:"source_type,omitempty" description:"MIME type or format"`
		Content    string            `json:"content" description:"Document text content"`
		Metadata   map[string]string `json:"metadata,omitempty" description:"Custom metadata"`
	} `json:"documents" description:"Documents to ingest"`
}

// GetDocumentRequest is the request for getting a document by ID.
type GetDocumentRequest struct {
	DocumentID string `path:"documentId" description:"Document ID"`
}

// ListDocumentsRequest is the request for listing documents.
type ListDocumentsRequest struct {
	CollectionID string `path:"collectionId" description:"Collection ID"`
	State        string `query:"state" description:"Filter by document state (pending, processing, ready, failed)"`
	Limit        int    `query:"limit" description:"Maximum number of results (default: 50)"`
	Offset       int    `query:"offset" description:"Number of results to skip"`
}

// DeleteDocumentRequest is the request for deleting a document.
type DeleteDocumentRequest struct {
	DocumentID string `path:"documentId" description:"Document ID"`
}

// ──────────────────────────────────────────────────
// Retrieval requests
// ──────────────────────────────────────────────────

// RetrieveRequest is the request body for retrieval.
type RetrieveRequest struct {
	CollectionID string  `path:"collectionId" description:"Collection ID"`
	Query        string  `json:"query" description:"The search query"`
	TopK         int     `json:"top_k,omitempty" description:"Maximum number of results"`
	MinScore     float64 `json:"min_score,omitempty" description:"Minimum relevance score threshold"`
	Strategy     string  `json:"strategy,omitempty" description:"Retrieval strategy (similarity, mmr, hybrid)"`
}

// HybridSearchRequest is the request body for hybrid search.
type HybridSearchRequest struct {
	Query       string              `json:"query" description:"The search query"`
	Collections []id.CollectionID   `json:"collections,omitempty" description:"Collection IDs to search"`
	TopK        int                 `json:"top_k,omitempty" description:"Maximum number of results"`
	Strategy    string              `json:"strategy,omitempty" description:"Search strategy"`
	MinScore    float64             `json:"min_score,omitempty" description:"Minimum relevance score threshold"`
}

// ──────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────

func defaultLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}
