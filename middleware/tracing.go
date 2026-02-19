package middleware

import (
	"context"
	"fmt"

	"github.com/xraph/weave/pipeline"
)

// TracingHook is called before and after each step for custom tracing.
type TracingHook interface {
	// BeforeStep is called before a pipeline step executes.
	BeforeStep(ctx context.Context, stepName string)
	// AfterStep is called after a pipeline step completes.
	AfterStep(ctx context.Context, stepName string, err error)
}

// Tracing returns a Middleware that calls the given hook before and
// after each pipeline step.
func Tracing(hook TracingHook) Middleware {
	return func(next StepHandler) StepHandler {
		return func(ctx context.Context, sc *pipeline.StepContext) error {
			stepName := sc.StepName()
			hook.BeforeStep(ctx, stepName)

			err := next(ctx, sc)
			hook.AfterStep(ctx, stepName, err)

			if err != nil {
				return fmt.Errorf("tracing: %w", err)
			}
			return nil
		}
	}
}

// LogTracer is a simple TracingHook that prints to a function.
type LogTracer struct {
	Printf func(format string, args ...any)
}

// BeforeStep logs the start of a step.
func (t *LogTracer) BeforeStep(_ context.Context, stepName string) {
	t.Printf("step %s: started", stepName)
}

// AfterStep logs the completion of a step.
func (t *LogTracer) AfterStep(_ context.Context, stepName string, err error) {
	if err != nil {
		t.Printf("step %s: failed: %v", stepName, err)
	} else {
		t.Printf("step %s: completed", stepName)
	}
}
