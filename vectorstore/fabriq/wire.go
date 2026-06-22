package fabriqvec

import (
	"github.com/xraph/fabriq"
	"github.com/xraph/vessel"

	"github.com/xraph/weave/engine"
)

// EngineOption auto-discovers a fabriq facade from the DI container and wires
// it as weave's vector store. Returns a no-op option when no facade is present.
func EngineOption(c vessel.Vessel, opts ...Option) engine.Option {
	f, err := vessel.Inject[*fabriq.Fabriq](c)
	if err != nil {
		return func(_ *engine.Engine) error { return nil }
	}
	return engine.WithVectorStore(New(f.Vector(), opts...))
}
