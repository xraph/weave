package middleware

import (
	"context"

	"github.com/xraph/weave"
	"github.com/xraph/weave/pipeline"
)

// Tenant returns a Middleware that extracts the tenant from the context
// and injects it into the pipeline step context under the standard
// tenant_id key.
func Tenant() Middleware {
	return func(next StepHandler) StepHandler {
		return func(ctx context.Context, sc *pipeline.StepContext) error {
			tenantID := weave.TenantFromContext(ctx)
			if tenantID != "" {
				sc.Set(pipeline.KeyTenantID, tenantID)
			}

			appID := weave.AppFromContext(ctx)
			if appID != "" {
				sc.Set("app_id", appID)
			}

			return next(ctx, sc)
		}
	}
}
