// Package id provides TypeID-based identity types for all Weave entities.
//
// Every entity in Weave gets a type-prefixed, K-sortable, UUIDv7-based
// identifier. IDs are validated at parse time to ensure the prefix matches
// the expected type.
//
// Examples:
//
//	doc_01h2xcejqtf2nbrexx3vqjhp41
//	col_01h2xcejqtf2nbrexx3vqjhp41
//	chk_01h455vb4pex5vsknk084sn02q
package id

import (
	"fmt"

	"go.jetify.com/typeid/v2"
)

// ──────────────────────────────────────────────────
// Prefix constants
// ──────────────────────────────────────────────────

const (
	// PrefixDocument is the TypeID prefix for documents.
	PrefixDocument = "doc"

	// PrefixCollection is the TypeID prefix for collections.
	PrefixCollection = "col"

	// PrefixChunk is the TypeID prefix for chunks.
	PrefixChunk = "chk"

	// PrefixPipeline is the TypeID prefix for pipelines.
	PrefixPipeline = "pipe"

	// PrefixIngestJob is the TypeID prefix for ingest jobs.
	PrefixIngestJob = "ingjob"
)

// ──────────────────────────────────────────────────
// Type aliases for readability
// ──────────────────────────────────────────────────

// DocumentID is a type-safe identifier for documents (prefix: "doc").
type DocumentID = typeid.TypeID

// CollectionID is a type-safe identifier for collections (prefix: "col").
type CollectionID = typeid.TypeID

// ChunkID is a type-safe identifier for chunks (prefix: "chk").
type ChunkID = typeid.TypeID

// PipelineID is a type-safe identifier for pipelines (prefix: "pipe").
type PipelineID = typeid.TypeID

// IngestJobID is a type-safe identifier for ingest jobs (prefix: "ingjob").
type IngestJobID = typeid.TypeID

// AnyID is a TypeID that accepts any valid prefix.
type AnyID = typeid.TypeID

// ──────────────────────────────────────────────────
// Constructors
// ──────────────────────────────────────────────────

// NewDocumentID returns a new random DocumentID.
func NewDocumentID() DocumentID { return must(typeid.Generate(PrefixDocument)) }

// NewCollectionID returns a new random CollectionID.
func NewCollectionID() CollectionID { return must(typeid.Generate(PrefixCollection)) }

// NewChunkID returns a new random ChunkID.
func NewChunkID() ChunkID { return must(typeid.Generate(PrefixChunk)) }

// NewPipelineID returns a new random PipelineID.
func NewPipelineID() PipelineID { return must(typeid.Generate(PrefixPipeline)) }

// NewIngestJobID returns a new random IngestJobID.
func NewIngestJobID() IngestJobID { return must(typeid.Generate(PrefixIngestJob)) }

// ──────────────────────────────────────────────────
// Parsing (validates prefix at parse time)
// ──────────────────────────────────────────────────

// ParseDocumentID parses a string into a DocumentID. Returns an error if the
// prefix is not "doc" or the suffix is invalid.
func ParseDocumentID(s string) (DocumentID, error) { return parseWithPrefix(PrefixDocument, s) }

// ParseCollectionID parses a string into a CollectionID.
func ParseCollectionID(s string) (CollectionID, error) { return parseWithPrefix(PrefixCollection, s) }

// ParseChunkID parses a string into a ChunkID.
func ParseChunkID(s string) (ChunkID, error) { return parseWithPrefix(PrefixChunk, s) }

// ParsePipelineID parses a string into a PipelineID.
func ParsePipelineID(s string) (PipelineID, error) { return parseWithPrefix(PrefixPipeline, s) }

// ParseIngestJobID parses a string into an IngestJobID.
func ParseIngestJobID(s string) (IngestJobID, error) { return parseWithPrefix(PrefixIngestJob, s) }

// ParseAny parses a string into an AnyID, accepting any valid prefix.
func ParseAny(s string) (AnyID, error) { return typeid.Parse(s) }

// ──────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────

// parseWithPrefix parses a TypeID and validates that its prefix matches expected.
func parseWithPrefix(expected, s string) (typeid.TypeID, error) {
	tid, err := typeid.Parse(s)
	if err != nil {
		return tid, err
	}
	if tid.Prefix() != expected {
		return tid, fmt.Errorf("id: expected prefix %q, got %q", expected, tid.Prefix())
	}
	return tid, nil
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
