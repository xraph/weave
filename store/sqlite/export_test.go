package sqlite

import (
	"context"

	"github.com/xraph/weave/document"
)

// PlantDocument writes doc as it is, with none of the store's checks, so the
// legacy conformance cases can plant rows only a v1.8 install could write.
func PlantDocument(ctx context.Context, s *Store, doc *document.Document) error {
	_, err := s.sdb.NewInsert(documentToModel(doc)).Exec(ctx)
	return err
}

// BeginWrite opens a store write transaction and returns the function that
// rolls it back.
func BeginWrite(ctx context.Context, s *Store) (func(), error) {
	tx, err := s.beginWrite(ctx)
	if err != nil {
		return nil, err
	}
	return func() { rollback(tx) }, nil
}
