package memory

import (
	"context"

	"github.com/xraph/weave/document"
)

// PlantDocument writes doc as it is, with none of the store's checks, so the
// legacy conformance cases can plant rows only a v1.8 install could write.
func PlantDocument(_ context.Context, s *Store, doc *document.Document) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *doc
	s.documents[doc.ID.String()] = &cp
	return nil
}
