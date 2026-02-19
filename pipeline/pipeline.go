package pipeline

import (
	"context"
	"fmt"
)

// Pipeline is an ordered sequence of steps executed in series.
type Pipeline struct {
	name  string
	steps []Step
}

// New creates a new pipeline with the given name.
func New(name string, steps ...Step) *Pipeline {
	return &Pipeline{name: name, steps: steps}
}

// Append adds steps to the pipeline.
func (p *Pipeline) Append(steps ...Step) {
	p.steps = append(p.steps, steps...)
}

// Name returns the pipeline name.
func (p *Pipeline) Name() string { return p.name }

// Steps returns the pipeline steps.
func (p *Pipeline) Steps() []Step { return p.steps }

// Run executes all steps in order. If any step returns an error,
// execution stops and the error is returned.
func (p *Pipeline) Run(ctx context.Context, sc *StepContext) error {
	if sc == nil {
		sc = NewStepContext()
	}

	for _, step := range p.steps {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pipeline %s: cancelled: %w", p.name, err)
		}

		sc.SetStepName(step.Name())
		if err := step.Run(ctx, sc); err != nil {
			return fmt.Errorf("pipeline %s: step %s: %w", p.name, step.Name(), err)
		}
	}
	return nil
}
