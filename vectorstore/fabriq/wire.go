package fabriqvec

import (
	"github.com/xraph/vessel"

	"github.com/xraph/fabriq/core/query"

	"github.com/xraph/weave/engine"
)

// EngineOption auto-discovers a fabriq facade from the DI container and wires
// it as weave's vector store. Returns a no-op option when no facade is present.
//
// The container is keyed on query.Fabric, the port, not on the concrete
// *fabriq.Fabriq. Naming the concrete facade would link fabriq's composition
// root, and with it every adapter it wires. It also means an engine reached
// over the wire (*remote.Fabric, same port) drops in here unchanged.
func EngineOption(c vessel.Vessel, opts ...Option) engine.Option {
	f, err := vessel.Inject[query.Fabric](c)
	if err != nil {
		return func(_ *engine.Engine) error { return nil }
	}
	return engine.WithVectorStore(New(f.Vector(), opts...))
}
