package tenancy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/internal/tenancy"
)

func ptr(s string) *string { return &s }

func TestScope(t *testing.T) {
	cases := []struct {
		name    string
		ctx     string
		filter  *string
		want    string
		wantErr error
	}{
		{"no tenant, no filter", "", nil, "", weave.ErrTenantRequired},
		{"tenant, no filter", "t1", nil, "t1", nil},
		{"filter equals the tenant", "t1", ptr("t1"), "t1", nil},
		{"filter names another tenant", "t1", ptr("t2"), "", weave.ErrInvalidArgument},
		{"filter is empty", "t1", ptr(""), "", weave.ErrInvalidArgument},
		{"filter without a context tenant", "", ptr("t1"), "", weave.ErrTenantRequired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.ctx != "" {
				ctx = weave.WithTenant(ctx, tc.ctx)
			}
			got, err := tenancy.Scope(ctx, tc.filter)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Scope: got (%q, %v), want %v", got, err, tc.wantErr)
				}
				if got != "" {
					t.Errorf("Scope: returned tenant %q with an error", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Scope: got (%q, %v), want (%q, nil)", got, err, tc.want)
			}
		})
	}
}

func TestOwner(t *testing.T) {
	t1 := weave.WithTenant(context.Background(), "t1")
	if got, err := tenancy.Owner(t1, "collection", "t1"); err != nil || got != "t1" {
		t.Errorf("Owner(t1, t1): got (%q, %v), want (\"t1\", nil)", got, err)
	}
	for _, owner := range []string{"t2", ""} {
		_, err := tenancy.Owner(t1, "collection", owner)
		if !errors.Is(err, weave.ErrInvalidArgument) {
			t.Errorf("Owner(t1, %q): got %v, want ErrInvalidArgument", owner, err)
		}
	}
	want := "weave: invalid argument: tenant on the collection does not match the context tenant"
	if _, err := tenancy.Owner(t1, "collection", "t2"); err == nil || err.Error() != want {
		t.Errorf("Owner message: got %v, want %q", err, want)
	}
	if _, err := tenancy.Owner(context.Background(), "collection", "t1"); !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("Owner without a context tenant: got %v, want ErrTenantRequired", err)
	}
}

func TestRequireTenant(t *testing.T) {
	if _, err := weave.RequireTenant(context.Background()); !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("no tenant: got %v, want ErrTenantRequired", err)
	}
	if _, err := weave.RequireTenant(weave.WithTenant(context.Background(), "")); !errors.Is(err, weave.ErrTenantRequired) {
		t.Errorf("empty tenant: got %v, want ErrTenantRequired", err)
	}
	if got, err := weave.RequireTenant(weave.WithTenant(context.Background(), "t1")); err != nil || got != "t1" {
		t.Errorf("t1: got (%q, %v), want (\"t1\", nil)", got, err)
	}
}

// TestFilterHelpers runs each filter helper with a nil filter, a filter with
// no Tenant, a matching Tenant and a mismatched one.
func TestFilterHelpers(t *testing.T) {
	t1 := weave.WithTenant(context.Background(), "t1")
	helpers := map[string]func(ctx context.Context, tenant *string, isNil bool) (string, error){
		"CollectionList": func(ctx context.Context, tenant *string, isNil bool) (string, error) {
			if isNil {
				return tenancy.CollectionList(ctx, nil)
			}
			return tenancy.CollectionList(ctx, &collection.ListFilter{Tenant: tenant})
		},
		"CollectionCount": func(ctx context.Context, tenant *string, isNil bool) (string, error) {
			if isNil {
				return tenancy.CollectionCount(ctx, nil)
			}
			return tenancy.CollectionCount(ctx, &collection.CountFilter{Tenant: tenant})
		},
		"DocumentList": func(ctx context.Context, tenant *string, isNil bool) (string, error) {
			if isNil {
				return tenancy.DocumentList(ctx, nil)
			}
			return tenancy.DocumentList(ctx, &document.ListFilter{Tenant: tenant})
		},
		"DocumentCount": func(ctx context.Context, tenant *string, isNil bool) (string, error) {
			if isNil {
				return tenancy.DocumentCount(ctx, nil)
			}
			return tenancy.DocumentCount(ctx, &document.CountFilter{Tenant: tenant})
		},
		"ChunkList": func(ctx context.Context, tenant *string, isNil bool) (string, error) {
			if isNil {
				return tenancy.ChunkList(ctx, nil)
			}
			return tenancy.ChunkList(ctx, &chunk.ListFilter{Tenant: tenant})
		},
		"ChunkCount": func(ctx context.Context, tenant *string, isNil bool) (string, error) {
			if isNil {
				return tenancy.ChunkCount(ctx, nil)
			}
			return tenancy.ChunkCount(ctx, &chunk.CountFilter{Tenant: tenant})
		},
	}
	for name, h := range helpers {
		for _, ok := range []struct {
			label  string
			tenant *string
			isNil  bool
		}{{"nil filter", nil, true}, {"no Tenant", nil, false}, {"matching Tenant", ptr("t1"), false}} {
			if got, err := h(t1, ok.tenant, ok.isNil); err != nil || got != "t1" {
				t.Errorf("%s, %s: got (%q, %v), want (\"t1\", nil)", name, ok.label, got, err)
			}
		}
		if _, err := h(t1, ptr("t2"), false); !errors.Is(err, weave.ErrInvalidArgument) {
			t.Errorf("%s, mismatched Tenant: got %v, want ErrInvalidArgument", name, err)
		}
		if _, err := h(context.Background(), nil, true); !errors.Is(err, weave.ErrTenantRequired) {
			t.Errorf("%s, no context tenant: got %v, want ErrTenantRequired", name, err)
		}
	}
}
