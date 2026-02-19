package api

import (
	"errors"

	"github.com/xraph/forge"

	"github.com/xraph/weave"
)

// mapStoreError maps domain errors to Forge HTTP errors.
func mapStoreError(err error) error {
	if err == nil {
		return nil
	}
	if isNotFound(err) {
		return forge.NotFound(err.Error())
	}
	return err
}

func isNotFound(err error) bool {
	return errors.Is(err, weave.ErrCollectionNotFound) ||
		errors.Is(err, weave.ErrDocumentNotFound) ||
		errors.Is(err, weave.ErrChunkNotFound)
}
