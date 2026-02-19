package weave

import "errors"

var (
	// Store errors.
	ErrNoStore         = errors.New("weave: no store configured")
	ErrStoreClosed     = errors.New("weave: store closed")
	ErrMigrationFailed = errors.New("weave: migration failed")

	// Not found errors.
	ErrCollectionNotFound = errors.New("weave: collection not found")
	ErrDocumentNotFound   = errors.New("weave: document not found")
	ErrChunkNotFound      = errors.New("weave: chunk not found")
	ErrIngestJobNotFound  = errors.New("weave: ingest job not found")

	// Conflict errors.
	ErrCollectionAlreadyExists = errors.New("weave: collection already exists")
	ErrDocumentAlreadyExists   = errors.New("weave: document already exists")
	ErrDuplicateDocument       = errors.New("weave: duplicate document (same content hash)")

	// State errors.
	ErrInvalidState = errors.New("weave: invalid state transition")
	ErrEmptyContent = errors.New("weave: empty content")

	// Pipeline errors.
	ErrNoEmbedder    = errors.New("weave: no embedder configured")
	ErrNoVectorStore = errors.New("weave: no vector store configured")
	ErrNoChunker     = errors.New("weave: no chunker configured")
)
