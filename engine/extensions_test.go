package engine_test

import (
	"context"
	"slices"
	"testing"

	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/engine"
)

type auditExt struct{}

func (auditExt) Name() string { return "audit" }
func (auditExt) OnCollectionCreated(context.Context, *collection.Collection) error {
	return nil
}
func (auditExt) OnShutdown(context.Context) error { return nil }

type quietExt struct{}

func (quietExt) Name() string { return "quiet" }

func TestDescribeExtensions(t *testing.T) {
	e := newTestEngine(t, engine.WithExtension(auditExt{}), engine.WithExtension(quietExt{}))
	got := e.DescribeExtensions()
	if len(got) != 2 {
		t.Fatalf("got %d extensions, want 2", len(got))
	}
	if got[0].Name != "audit" || !slices.Equal(got[0].Hooks, []string{"collection_created", "shutdown"}) {
		t.Errorf("audit: %+v", got[0])
	}
	if got[1].Name != "quiet" || got[1].Hooks == nil || len(got[1].Hooks) != 0 {
		t.Errorf("quiet: %+v (hooks must be an empty list, not null)", got[1])
	}
}
