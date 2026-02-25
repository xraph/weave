// Package id defines TypeID-based identity types for all Weave entities.
//
// Every entity in Weave uses a single ID struct with a prefix that identifies
// the entity type. IDs are K-sortable (UUIDv7-based), globally unique,
// and URL-safe in the format "prefix_suffix".
package id

import (
	"database/sql/driver"
	"fmt"

	"go.jetify.com/typeid/v2"
)

// Prefix identifies the entity type encoded in a TypeID.
type Prefix string

// Prefix constants for all Weave entity types.
const (
	PrefixDocument   Prefix = "doc"
	PrefixCollection Prefix = "col"
	PrefixChunk      Prefix = "chk"
	PrefixPipeline   Prefix = "pipe"
	PrefixIngestJob  Prefix = "ingjob"
)

// ID is the primary identifier type for all Weave entities.
// It wraps a TypeID providing a prefix-qualified, globally unique,
// sortable, URL-safe identifier in the format "prefix_suffix".
//
//nolint:recvcheck // Value receivers for read-only methods, pointer receivers for UnmarshalText/Scan.
type ID struct {
	inner typeid.TypeID
	valid bool
}

// Nil is the zero-value ID.
var Nil ID

// New generates a new globally unique ID with the given prefix.
// It panics if prefix is not a valid TypeID prefix (programming error).
func New(prefix Prefix) ID {
	tid, err := typeid.Generate(string(prefix))
	if err != nil {
		panic(fmt.Sprintf("id: invalid prefix %q: %v", prefix, err))
	}

	return ID{inner: tid, valid: true}
}

// Parse parses a TypeID string (e.g., "doc_01h2xcejqtf2nbrexx3vqjhp41")
// into an ID. Returns an error if the string is not valid.
func Parse(s string) (ID, error) {
	if s == "" {
		return Nil, fmt.Errorf("id: parse %q: empty string", s)
	}

	tid, err := typeid.Parse(s)
	if err != nil {
		return Nil, fmt.Errorf("id: parse %q: %w", s, err)
	}

	return ID{inner: tid, valid: true}, nil
}

// ParseWithPrefix parses a TypeID string and validates that its prefix
// matches the expected value.
func ParseWithPrefix(s string, expected Prefix) (ID, error) {
	parsed, err := Parse(s)
	if err != nil {
		return Nil, err
	}

	if parsed.Prefix() != expected {
		return Nil, fmt.Errorf("id: expected prefix %q, got %q", expected, parsed.Prefix())
	}

	return parsed, nil
}

// MustParse is like Parse but panics on error. Use for hardcoded ID values.
func MustParse(s string) ID {
	parsed, err := Parse(s)
	if err != nil {
		panic(fmt.Sprintf("id: must parse %q: %v", s, err))
	}

	return parsed
}

// MustParseWithPrefix is like ParseWithPrefix but panics on error.
func MustParseWithPrefix(s string, expected Prefix) ID {
	parsed, err := ParseWithPrefix(s, expected)
	if err != nil {
		panic(fmt.Sprintf("id: must parse with prefix %q: %v", expected, err))
	}

	return parsed
}

// ──────────────────────────────────────────────────
// Type aliases for backward compatibility
// ──────────────────────────────────────────────────

// DocumentID is a type-safe identifier for documents (prefix: "doc").
type DocumentID = ID

// CollectionID is a type-safe identifier for collections (prefix: "col").
type CollectionID = ID

// ChunkID is a type-safe identifier for chunks (prefix: "chk").
type ChunkID = ID

// PipelineID is a type-safe identifier for pipelines (prefix: "pipe").
type PipelineID = ID

// IngestJobID is a type-safe identifier for ingest jobs (prefix: "ingjob").
type IngestJobID = ID

// AnyID is a type alias that accepts any valid prefix.
type AnyID = ID

// ──────────────────────────────────────────────────
// Convenience constructors
// ──────────────────────────────────────────────────

// NewDocumentID generates a new unique document ID.
func NewDocumentID() ID { return New(PrefixDocument) }

// NewCollectionID generates a new unique collection ID.
func NewCollectionID() ID { return New(PrefixCollection) }

// NewChunkID generates a new unique chunk ID.
func NewChunkID() ID { return New(PrefixChunk) }

// NewPipelineID generates a new unique pipeline ID.
func NewPipelineID() ID { return New(PrefixPipeline) }

// NewIngestJobID generates a new unique ingest job ID.
func NewIngestJobID() ID { return New(PrefixIngestJob) }

// ──────────────────────────────────────────────────
// Convenience parsers
// ──────────────────────────────────────────────────

// ParseDocumentID parses a string and validates the "doc" prefix.
func ParseDocumentID(s string) (ID, error) { return ParseWithPrefix(s, PrefixDocument) }

// ParseCollectionID parses a string and validates the "col" prefix.
func ParseCollectionID(s string) (ID, error) { return ParseWithPrefix(s, PrefixCollection) }

// ParseChunkID parses a string and validates the "chk" prefix.
func ParseChunkID(s string) (ID, error) { return ParseWithPrefix(s, PrefixChunk) }

// ParsePipelineID parses a string and validates the "pipe" prefix.
func ParsePipelineID(s string) (ID, error) { return ParseWithPrefix(s, PrefixPipeline) }

// ParseIngestJobID parses a string and validates the "ingjob" prefix.
func ParseIngestJobID(s string) (ID, error) { return ParseWithPrefix(s, PrefixIngestJob) }

// ParseAny parses a string into an ID without type checking the prefix.
func ParseAny(s string) (ID, error) { return Parse(s) }

// ──────────────────────────────────────────────────
// ID methods
// ──────────────────────────────────────────────────

// String returns the full TypeID string representation (prefix_suffix).
// Returns an empty string for the Nil ID.
func (i ID) String() string {
	if !i.valid {
		return ""
	}

	return i.inner.String()
}

// Prefix returns the prefix component of this ID.
func (i ID) Prefix() Prefix {
	if !i.valid {
		return ""
	}

	return Prefix(i.inner.Prefix())
}

// IsNil reports whether this ID is the zero value.
func (i ID) IsNil() bool {
	return !i.valid
}

// MarshalText implements encoding.TextMarshaler.
func (i ID) MarshalText() ([]byte, error) {
	if !i.valid {
		return []byte{}, nil
	}

	return []byte(i.inner.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (i *ID) UnmarshalText(data []byte) error {
	if len(data) == 0 {
		*i = Nil

		return nil
	}

	parsed, err := Parse(string(data))
	if err != nil {
		return err
	}

	*i = parsed

	return nil
}

// Value implements driver.Valuer for database storage.
// Returns nil for the Nil ID so that optional foreign key columns store NULL.
func (i ID) Value() (driver.Value, error) {
	if !i.valid {
		return nil, nil //nolint:nilnil // nil is the canonical NULL for driver.Valuer
	}

	return i.inner.String(), nil
}

// Scan implements sql.Scanner for database retrieval.
func (i *ID) Scan(src any) error {
	if src == nil {
		*i = Nil

		return nil
	}

	switch v := src.(type) {
	case string:
		if v == "" {
			*i = Nil

			return nil
		}

		return i.UnmarshalText([]byte(v))
	case []byte:
		if len(v) == 0 {
			*i = Nil

			return nil
		}

		return i.UnmarshalText(v)
	default:
		return fmt.Errorf("id: cannot scan %T into ID", src)
	}
}
