package engine

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/xraph/weave"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/id"
)

// CollectionUpdate changes the parts of a collection that are safe to change
// after ingestion. A nil field is left alone. Chunk size, overlap and the
// recorded embedding model, dimensions and strategy are deliberately absent:
// Weave cannot re-chunk existing documents, so changing them would leave
// every existing chunk built under different settings from new ones.
type CollectionUpdate struct {
	Name        *string
	Description *string
	Metadata    *map[string]string
}

// UpdateCollection applies upd and returns the stored collection.
func (e *Engine) UpdateCollection(ctx context.Context, colID id.CollectionID, upd CollectionUpdate) (*collection.Collection, error) {
	if e.store == nil {
		return nil, weave.ErrNoStore
	}
	if upd.Name != nil && strings.TrimSpace(*upd.Name) == "" {
		return nil, fmt.Errorf("%w: collection name cannot be blank", weave.ErrInvalidArgument)
	}

	current, err := e.store.GetCollection(ctx, colID)
	if err != nil {
		return nil, err
	}
	// Work on a copy: the memory store hands back its own pointer, and a
	// refused write must not leave a half-applied change behind.
	next := *current
	if upd.Name != nil {
		next.Name = strings.TrimSpace(*upd.Name)
	}
	if upd.Description != nil {
		next.Description = *upd.Description
	}
	if upd.Metadata != nil {
		next.Metadata = maps.Clone(*upd.Metadata)
		if next.Metadata == nil {
			next.Metadata = map[string]string{}
		}
	}
	if err := e.store.UpdateCollection(ctx, &next); err != nil {
		return nil, err
	}
	return e.store.GetCollection(ctx, colID)
}
