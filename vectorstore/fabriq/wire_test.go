package fabriqvec

import (
	"testing"

	"github.com/xraph/vessel"

	"github.com/xraph/weave/engine"
)

func TestEngineOption_NoFacadeIsNoop(t *testing.T) {
	c := vessel.New() // empty container, no *fabriq.Fabriq
	opt := EngineOption(c)
	if opt == nil {
		t.Fatalf("EngineOption returned nil; want a no-op engine.Option")
	}
	// Applying the no-op option to a fresh engine must not error.
	_, err := engine.New(opt)
	if err != nil {
		t.Fatalf("engine.New with no-op EngineOption: %v", err)
	}
}
