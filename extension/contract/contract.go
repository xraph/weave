// Package contract wires Weave into the Forge dashboard's contract path. It
// registers the `weave` contributor and answers its intents from the Weave
// engine. Handlers call engine methods only, never the store, so a fix in
// the engine reaches the dashboard and Weave's HTTP API alike.
package contract

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/xraph/forge"
	"github.com/xraph/forge/extensions/dashboard/contract"
	"github.com/xraph/forge/extensions/dashboard/contract/dispatcher"
	"github.com/xraph/forge/extensions/dashboard/contract/loader"

	"github.com/xraph/weave/engine"
)

//go:embed manifest.yaml
var manifestYAML []byte

// ContributorName is the join key with packages/plugin-weave's `extension`
// field and matches the extension's name. A mismatch hides the React
// plugin with no error anywhere.
const ContributorName = "weave"

// StalledAfter is how long a document may sit in processing before the
// dashboard flags it. Ingest runs inside one request and Weave has no
// heartbeat, so a document still processing this long after its last
// update almost certainly died with its process.
const StalledAfter = 15 * time.Minute

// Deps bundles what the handlers need.
type Deps struct {
	// Engine answers every intent. Required.
	Engine *engine.Engine

	// Logger receives an Error entry for every error mapped to
	// CodeInternal. Optional.
	Logger forge.Logger

	// Now is the clock the stalled check reads. Optional; nil means
	// time.Now.
	Now func() time.Time
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// binding registers one intent with the dispatcher.
type binding struct {
	intent string
	bind   func(d *dispatcher.Dispatcher) error
}

func query[I, O any](intent string, fn func(context.Context, I, contract.Principal) (O, error)) binding {
	return binding{intent: intent, bind: func(d *dispatcher.Dispatcher) error {
		return dispatcher.RegisterQuery(d, ContributorName, intent, 1, fn)
	}}
}

func command[I, O any](intent string, fn func(context.Context, I, contract.Principal) (O, error)) binding {
	return binding{intent: intent, bind: func(d *dispatcher.Dispatcher) error {
		return dispatcher.RegisterCommand(d, ContributorName, intent, 1, fn)
	}}
}

// bindings lists every intent this package answers. Tasks 4 to 8 add to it.
func bindings(deps Deps) []binding {
	return []binding{
		query("system.overview", systemOverviewHandler(deps)),
		query("system.components", systemComponentsHandler(deps)),
		query("collections.list", collectionsListHandler(deps)),
		query("collections.get", collectionsGetHandler(deps)),
		command("collections.create", collectionsCreateHandler(deps)),
		command("collections.update", collectionsUpdateHandler(deps)),
		command("collections.delete", collectionsDeleteHandler(deps)),
		command("collections.reindex", collectionsReindexHandler(deps)),
	}
}

// Register loads and validates the embedded manifest, registers the
// `weave` contributor with reg, and binds every handler. A handler bound
// to an intent the manifest does not declare is a build mistake and fails
// here rather than at the first request.
func Register(d *dispatcher.Dispatcher, reg contract.Registry, wreg contract.WardenRegistry, deps Deps) error {
	if deps.Engine == nil {
		return fmt.Errorf("weave/contract: Engine is required")
	}
	m, err := loader.Load(bytes.NewReader(manifestYAML), "weave/contract/manifest.yaml")
	if err != nil {
		return fmt.Errorf("weave/contract: load manifest: %w", err)
	}
	if err := loader.Validate(m, wreg); err != nil {
		return fmt.Errorf("weave/contract: validate manifest: %w", err)
	}
	if err := reg.Register(m); err != nil {
		return fmt.Errorf("weave/contract: register manifest: %w", err)
	}
	declared := make(map[string]bool, len(m.Intents))
	for _, in := range m.Intents {
		declared[in.Name] = true
	}
	for _, b := range bindings(deps) {
		if !declared[b.intent] {
			return fmt.Errorf("weave/contract: %s is bound but the manifest does not declare it", b.intent)
		}
		if err := b.bind(d); err != nil {
			return fmt.Errorf("weave/contract: register %s: %w", b.intent, err)
		}
	}
	return nil
}
