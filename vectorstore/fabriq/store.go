// Package fabriqvec provides a fabriq VectorQuerier-backed implementation of
// the vectorstore.VectorStore interface. Each weave Entry is stored as a
// fabriq vector embedding keyed by Entry.ID, with the Entry's Content folded
// into the meta map under the key "content" and all remaining Metadata
// key/value pairs included alongside it.
package fabriqvec

import (
	"context"
	"fmt"

	"github.com/xraph/fabriq/core/query"
	"github.com/xraph/fabriq/core/tenant"

	"github.com/xraph/weave"
	"github.com/xraph/weave/internal/tenancy"
	"github.com/xraph/weave/vectorstore"
)

const (
	defaultEntity = "weave_vector"
	contentKey    = "content"
)

// Compile-time interface check.
var _ vectorstore.VectorStore = (*Store)(nil)

// Store bridges weave's VectorStore interface to fabriq's VectorQuerier port.
type Store struct {
	vq     query.VectorQuerier
	entity string
}

// Option configures the Store.
type Option func(*Store)

// WithEntity sets a custom fabriq entity name (default: "weave_vector").
func WithEntity(name string) Option {
	return func(s *Store) {
		s.entity = name
	}
}

// New creates a new fabriqvec Store backed by the given VectorQuerier.
func New(vq query.VectorQuerier, opts ...Option) *Store {
	s := &Store{
		vq:     vq,
		entity: defaultEntity,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Upsert inserts or updates vector entries. Each Entry's Content is stored
// under the "content" meta key; remaining Metadata keys are stored alongside.
// An entry without metadata["tenant_id"] is ErrTenantRequired, and nothing
// in the batch is written.
//
// Each entry is written under the Fabriq tenant its tenant_id names, which
// replaces any Fabriq tenant already on ctx: the engine labels every entry
// with the weave tenant and sets no Fabriq tenant of its own. A tenant_id
// Fabriq cannot accept as a tenant ID refuses the batch before anything is
// written.
func (s *Store) Upsert(ctx context.Context, entries []vectorstore.Entry) error {
	if err := tenancy.VectorEntries(entries); err != nil {
		return err
	}
	scoped := make([]context.Context, len(entries))
	for i, e := range entries {
		tctx, err := tenant.WithTenant(ctx, e.Metadata["tenant_id"])
		if err != nil {
			return fmt.Errorf("fabriqvec: upsert %q: set tenant: %w", e.ID, err)
		}
		scoped[i] = tctx
	}
	for i, e := range entries {
		meta := make(map[string]any, len(e.Metadata)+1)
		for k, v := range e.Metadata {
			meta[k] = v
		}
		meta[contentKey] = e.Content
		if err := s.vq.Upsert(scoped[i], s.entity, e.ID, e.Vector, meta); err != nil {
			return fmt.Errorf("fabriqvec: upsert %q: %w", e.ID, err)
		}
	}
	return nil
}

// Search returns the most similar entries to the given vector. TenantKey is
// required (ErrTenantRequired without it) and scopes the search via
// tenant.WithTenant; MinScore post-filters results below the threshold;
// SearchResult.Vector is always nil (fabriq VectorQuerier does not return
// stored embeddings from Similar).
func (s *Store) Search(ctx context.Context, vector []float32, opts *vectorstore.SearchOptions) ([]vectorstore.SearchResult, error) {
	if err := tenancy.VectorSearch(opts); err != nil {
		return nil, err
	}
	topK := 10
	var filter map[string]string
	var minScore float64

	if opts != nil {
		if opts.TopK > 0 {
			topK = opts.TopK
		}
		if opts.MinScore > 0 {
			minScore = opts.MinScore
		}
		if len(opts.Filter) > 0 {
			filter = opts.Filter
		}
		if opts.TenantKey != "" {
			tctx, err := tenant.WithTenant(ctx, opts.TenantKey)
			if err != nil {
				return nil, fmt.Errorf("fabriqvec: set tenant %q: %w", opts.TenantKey, err)
			}
			ctx = tctx
		}
	}

	q := query.VectorQuery{
		Entity:    s.entity,
		Embedding: vector,
		K:         topK,
		Filter:    filter,
	}

	var matches []query.VectorMatch
	if err := s.vq.Similar(ctx, q, &matches); err != nil {
		return nil, fmt.Errorf("fabriqvec: similar: %w", err)
	}

	results := make([]vectorstore.SearchResult, 0, len(matches))
	for _, m := range matches {
		if minScore > 0 && m.Score < minScore {
			continue
		}

		// Reconstruct weave Entry from fabriq meta.
		content, err := m.Meta[contentKey].(string)
		_ = err

		metadata := make(map[string]string, len(m.Meta))
		for k, v := range m.Meta {
			if k == contentKey {
				continue
			}
			metadata[k] = fmt.Sprint(v)
		}

		results = append(results, vectorstore.SearchResult{
			Entry: vectorstore.Entry{
				ID:       m.ID,
				Vector:   nil, // not returned by fabriq Similar
				Content:  content,
				Metadata: metadata,
			},
			Score: m.Score,
		})
	}
	return results, nil
}

// Delete removes entries by their IDs, under the Fabriq tenant of the weave
// tenant on ctx (weave.WithTenant), as the engine calls it. Without one it
// uses a Fabriq tenant already on ctx, and with neither it is
// ErrTenantRequired.
func (s *Store) Delete(ctx context.Context, ids []string) error {
	tctx, err := deleteScope(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.vq.Delete(tctx, s.entity, id); err != nil {
			return fmt.Errorf("fabriqvec: delete %q: %w", id, err)
		}
	}
	return nil
}

// DeleteByMetadata removes entries whose meta matches all key/value pairs in
// filter. Delegates to fabriq's DeleteByMeta (AND-of-equals semantics) under
// the Fabriq tenant filter["tenant_id"] names, which the engine always sends.
// An empty filter is ErrInvalidArgument and a filter without tenant_id is
// ErrTenantRequired.
func (s *Store) DeleteByMetadata(ctx context.Context, filter map[string]string) error {
	if err := tenancy.VectorDeleteFilter(filter); err != nil {
		return err
	}
	tenantID := filter["tenant_id"]
	if tenantID == "" {
		return fmt.Errorf("fabriqvec: delete by meta: %w", weave.ErrTenantRequired)
	}
	tctx, err := tenant.WithTenant(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("fabriqvec: delete by meta: set tenant: %w", err)
	}
	if err := s.vq.DeleteByMeta(tctx, s.entity, filter); err != nil {
		return fmt.Errorf("fabriqvec: delete by meta: %w", err)
	}
	return nil
}

// deleteScope returns ctx stamped with the Fabriq tenant Delete runs under.
func deleteScope(ctx context.Context) (context.Context, error) {
	if weaveTenant := weave.TenantFromContext(ctx); weaveTenant != "" {
		tctx, err := tenant.WithTenant(ctx, weaveTenant)
		if err != nil {
			return nil, fmt.Errorf("fabriqvec: delete: set tenant: %w", err)
		}
		return tctx, nil
	}
	if _, err := tenant.FromContext(ctx); err != nil {
		return nil, fmt.Errorf("fabriqvec: delete: %w", weave.ErrTenantRequired)
	}
	return ctx, nil
}
