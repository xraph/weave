package engine

import (
	"context"
	"fmt"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
)

// tenant is the entry check every engine method that touches data runs
// first: nothing reaches the embedder, the vector store or the metadata
// store before it passes. It returns ctx carrying the tenant, and the tenant.
func (e *Engine) tenant(ctx context.Context) (context.Context, string, error) {
	tenant, err := weave.RequireTenant(ctx)
	if err != nil {
		return ctx, "", err
	}
	return ctx, tenant, nil
}

// tenantNamed is tenant for the calls that still take a deprecated tenant
// option (WithTenantID, WithTenantFilter, CompareParams.Tenant). With no
// context tenant, a named one becomes the tenant and is put on the returned
// context, so every read after it is scoped. With a context tenant, every
// named value must equal it. A nil entry was not given; WithTenantID("")
// arrives as nil, WithTenantFilter("") as a pointer to "".
func (e *Engine) tenantNamed(ctx context.Context, named ...*string) (context.Context, string, error) {
	picked, given := "", false
	for _, n := range named {
		switch {
		case n == nil:
		case !given:
			picked, given = *n, true
		case *n != picked:
			return ctx, "", fmt.Errorf("%w: tenant options name different tenants", weave.ErrInvalidArgument)
		}
	}
	current := weave.TenantFromContext(ctx)
	switch {
	case !given:
	case current == "":
		if picked == "" {
			return ctx, "", weave.ErrTenantRequired
		}
		ctx = weave.WithTenant(ctx, picked)
	case picked != current:
		return ctx, "", fmt.Errorf("%w: tenant option does not match the context tenant", weave.ErrInvalidArgument)
	}
	return e.tenant(ctx)
}

// The checks below guard against a custom store that ignores the context
// tenant: a row it hands back from another tenant is the not-found sentinel,
// exactly as a built-in store would answer.

func ownCollection(col *collection.Collection, tenant string) (*collection.Collection, error) {
	if col == nil || col.TenantID != tenant {
		return nil, weave.ErrCollectionNotFound
	}
	return col, nil
}

func ownDocument(doc *document.Document, tenant string) (*document.Document, error) {
	if doc == nil || doc.TenantID != tenant {
		return nil, weave.ErrDocumentNotFound
	}
	return doc, nil
}

func ownChunk(ch *chunk.Chunk, tenant string) (*chunk.Chunk, error) {
	if ch == nil || ch.TenantID != tenant {
		return nil, weave.ErrChunkNotFound
	}
	return ch, nil
}

// owned keeps the rows whose tenant is tenant, in order. It never edits
// rows, which may be the store's own slice.
func owned[T any](rows []T, tenant string, of func(T) string) []T {
	out := rows[:0:0]
	for _, r := range rows {
		if of(r) == tenant {
			out = append(out, r)
		}
	}
	return out
}

func collectionTenant(c *collection.Collection) string { return c.TenantID }
func documentTenant(d *document.Document) string       { return d.TenantID }
func chunkTenant(c *chunk.Chunk) string                { return c.TenantID }

// vectorMetadata builds a vector entry's metadata: the chunk's own keys
// first, then the reserved keys tenancy and hydration rest on, so a
// chunker's metadata can never overwrite them.
func vectorMetadata(ch *chunk.Chunk) map[string]string {
	meta := make(map[string]string, len(ch.Metadata)+4)
	for k, v := range ch.Metadata {
		meta[k] = v
	}
	meta["collection_id"] = ch.CollectionID.String()
	meta["document_id"] = ch.DocumentID.String()
	meta["tenant_id"] = ch.TenantID
	meta["chunk_index"] = fmt.Sprintf("%d", ch.Index)
	return meta
}
