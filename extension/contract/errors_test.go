package contract

import (
	"context"
	"errors"
	"fmt"
	"testing"

	dashcontract "github.com/xraph/forge/extensions/dashboard/contract"

	"github.com/xraph/weave"
	"github.com/xraph/weave/id"
)

func TestMapError(t *testing.T) {
	cases := []struct {
		err  error
		code dashcontract.ErrorCode
	}{
		{weave.ErrCollectionNotFound, dashcontract.CodeNotFound},
		{fmt.Errorf("wrapped: %w", weave.ErrDocumentNotFound), dashcontract.CodeNotFound},
		{weave.ErrChunkNotFound, dashcontract.CodeNotFound},
		{weave.ErrDuplicateDocument, dashcontract.CodeConflict},
		{weave.ErrCollectionAlreadyExists, dashcontract.CodeConflict},
		{weave.ErrEmptyContent, dashcontract.CodeBadRequest},
		{fmt.Errorf("%w: list chunks needs a document or a collection", weave.ErrInvalidArgument), dashcontract.CodeBadRequest},
		{weave.ErrNoEmbedder, dashcontract.CodeUnavailable},
		{weave.ErrNoVectorStore, dashcontract.CodeUnavailable},
		{weave.ErrNoChunker, dashcontract.CodeUnavailable},
		{weave.ErrNoStore, dashcontract.CodeUnavailable},
		{context.Canceled, dashcontract.CodeUnavailable},
		{errors.New("dial tcp 10.0.0.7:5432: connection refused user=weave password=hunter2"), dashcontract.CodeInternal},
	}
	for _, tc := range cases {
		if got := codeOf(mapError(tc.err)); got != tc.code {
			t.Errorf("mapError(%v) = %s, want %s", tc.err, got, tc.code)
		}
	}
}

// An unrecognised error's text can carry a DSN, a host or a credential, so
// it never reaches the client.
func TestMapError_InternalHidesTheCause(t *testing.T) {
	var ce *dashcontract.Error
	if !errors.As(mapError(errors.New("password=hunter2")), &ce) {
		t.Fatal("not a contract error")
	}
	if ce.Message != "an internal error occurred" {
		t.Errorf("message = %q", ce.Message)
	}
}

func TestMapError_InvalidArgumentKeepsItsReason(t *testing.T) {
	var ce *dashcontract.Error
	errors.As(mapError(fmt.Errorf("%w: collection name cannot be blank", weave.ErrInvalidArgument)), &ce)
	if ce == nil || ce.Message != "collection name cannot be blank" {
		t.Errorf("got %+v", ce)
	}
}

func TestParseIDs(t *testing.T) {
	if _, err := parseCollectionID("id", ""); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("empty: %v", err)
	}
	if _, err := parseCollectionID("id", "not-an-id"); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("garbage: %v", err)
	}
	if _, err := parseCollectionID("id", id.NewDocumentID().String()); codeOf(err) != dashcontract.CodeBadRequest {
		t.Errorf("wrong prefix: %v", err)
	}
	good := id.NewCollectionID()
	got, err := parseCollectionID("id", good.String())
	if err != nil || got.String() != good.String() {
		t.Errorf("good: %v %v", got, err)
	}
	if got, err := optionalCollectionID("collection_id", ""); err != nil || !got.IsNil() {
		t.Errorf("optional empty: %v %v", got, err)
	}
}
