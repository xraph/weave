// Package pipeline provides a composable pipeline engine for orchestrating
// multi-step RAG operations (load, chunk, embed, store, retrieve, assemble).
package pipeline

import "context"

// StepContext carries accumulated state through pipeline steps.
type StepContext struct {
	// Values is a generic key-value store for passing data between steps.
	Values map[string]any
	// stepName is set by the pipeline runner before each step executes.
	stepName string
}

// NewStepContext creates an empty step context.
func NewStepContext() *StepContext {
	return &StepContext{Values: make(map[string]any)}
}

// Set stores a value in the step context.
func (sc *StepContext) Set(key string, value any) {
	sc.Values[key] = value
}

// Get retrieves a value from the step context.
func (sc *StepContext) Get(key string) (any, bool) {
	v, ok := sc.Values[key]
	return v, ok
}

// MustGet retrieves a value or panics if not found.
func (sc *StepContext) MustGet(key string) any {
	v, ok := sc.Values[key]
	if !ok {
		panic("pipeline: missing step context key: " + key)
	}
	return v
}

// StepName returns the name of the currently executing step.
func (sc *StepContext) StepName() string { return sc.stepName }

// SetStepName sets the current step name (called by the pipeline runner).
func (sc *StepContext) SetStepName(name string) { sc.stepName = name }

// Keys returns all keys stored in the context.
func (sc *StepContext) Keys() []string {
	keys := make([]string, 0, len(sc.Values))
	for k := range sc.Values {
		keys = append(keys, k)
	}
	return keys
}

// Snapshot returns a shallow copy of the current values.
func (sc *StepContext) Snapshot() map[string]any {
	snap := make(map[string]any, len(sc.Values))
	for k, v := range sc.Values {
		snap[k] = v
	}
	return snap
}

// SetCacheHit restores cached values into the step context.
func (sc *StepContext) SetCacheHit(stepName string, data any) {
	if m, ok := data.(map[string]any); ok {
		for k, v := range m {
			sc.Values[k] = v
		}
	}
}

// Step is a single unit of work in a pipeline.
type Step interface {
	// Name returns a human-readable name for this step.
	Name() string

	// Run executes the step, reading from and writing to the step context.
	Run(ctx context.Context, sc *StepContext) error
}
