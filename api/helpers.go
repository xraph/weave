package api

import (
	"errors"

	"github.com/xraph/forge"

	"github.com/xraph/weave"
)

// mapStoreError maps domain errors to Forge HTTP errors. A request without
// a tenant is 403, a not-found sentinel 404 and an invalid argument 400.
// Anything else is returned as it is, which Forge answers with a 500 and a
// generic message.
func mapStoreError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, weave.ErrTenantRequired):
		return forge.Forbidden("weave: this request has no tenant")
	case isNotFound(err):
		return forge.NotFound(err.Error())
	case errors.Is(err, weave.ErrInvalidArgument):
		return forge.BadRequest(err.Error())
	}
	return err
}

func isNotFound(err error) bool {
	return errors.Is(err, weave.ErrCollectionNotFound) ||
		errors.Is(err, weave.ErrDocumentNotFound) ||
		errors.Is(err, weave.ErrChunkNotFound)
}
