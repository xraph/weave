package weave

import "context"

type contextKey int

const (
	tenantKey contextKey = iota
	appKey
)

// TenantFromContext extracts the tenant identifier from the context.
// Returns an empty string if no tenant is set.
func TenantFromContext(ctx context.Context) string {
	v, _ := ctx.Value(tenantKey).(string) //nolint:errcheck // zero value is fine
	return v
}

// RequireTenant returns the tenant on ctx, or ErrTenantRequired when there is
// none. Every store call and every engine call that touches data needs one.
func RequireTenant(ctx context.Context) (string, error) {
	tenant := TenantFromContext(ctx)
	if tenant == "" {
		return "", ErrTenantRequired
	}
	return tenant, nil
}

// WithTenant returns a copy of ctx with the tenant identifier attached.
func WithTenant(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, tenantKey, tenant)
}

// AppFromContext extracts the app identifier from the context.
// Returns an empty string if no app is set.
func AppFromContext(ctx context.Context) string {
	v, _ := ctx.Value(appKey).(string) //nolint:errcheck // zero value is fine
	return v
}

// WithApp returns a copy of ctx with the app identifier attached.
func WithApp(ctx context.Context, app string) context.Context {
	return context.WithValue(ctx, appKey, app)
}
