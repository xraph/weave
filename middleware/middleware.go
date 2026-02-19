// Package middleware provides composable middleware for the Weave pipeline.
// Middleware wraps pipeline step execution, enabling cross-cutting concerns
// like caching, tracing, and tenant isolation.
package middleware

import (
	"context"

	"github.com/xraph/weave/pipeline"
)

// StepHandler executes a pipeline step.
type StepHandler func(ctx context.Context, sc *pipeline.StepContext) error

// Middleware wraps step execution.
type Middleware func(next StepHandler) StepHandler

// Chain composes multiple middleware into a single Middleware.
// Middleware are applied left-to-right: the first middleware in the
// chain is the outermost wrapper.
func Chain(mws ...Middleware) Middleware {
	return func(next StepHandler) StepHandler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}
