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

	"github.com/xraph/weave"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/internal/telemetry"
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

	// Tenant resolves the Weave tenant for a dashboard request. Nil reads
	// weave.TenantFromContext(ctx). An empty result or an error refuses the
	// request with PERMISSION_DENIED.
	Tenant func(ctx context.Context, p contract.Principal) (string, error)
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// noTenantMessage answers a request the dashboard can't place in a tenant.
// It says nothing about why: a resolver's error can name an organisation
// or a lookup the client has no business seeing.
const noTenantMessage = "this dashboard request has no Weave tenant"

// tenantFilterer is an input that carries the client's `tenant` field. The
// tenant wrapper refuses one that differs from the resolved tenant, and no
// handler reads it.
type tenantFilterer interface {
	tenantFilter() *string
}

// resolveTenant answers the tenant every intent runs under. A resolver's
// error is logged by type only and never reaches the client.
func (d Deps) resolveTenant(ctx context.Context, intent string, p contract.Principal) (string, error) {
	if d.Tenant == nil {
		if tenant := weave.TenantFromContext(ctx); tenant != "" {
			return tenant, nil
		}
		return "", permissionDenied(noTenantMessage)
	}
	tenant, err := d.Tenant(ctx, p)
	if err != nil {
		if d.Logger != nil {
			d.Logger.Warn("weave/contract: the dashboard tenant resolver failed",
				forge.F("intent", intent),
				forge.F("error_type", telemetry.ErrorType(err)),
			)
		}
		return "", permissionDenied(noTenantMessage)
	}
	if tenant == "" {
		return "", permissionDenied(noTenantMessage)
	}
	return tenant, nil
}

// withTenant runs fn under the tenant resolved for the request, so every
// engine call fn makes is scoped to it. A request with no tenant is refused
// before fn runs, and so is one whose `tenant` field names another tenant:
// the field can only repeat the resolved tenant, never pick one.
func withTenant[I, O any](deps Deps, intent string, fn func(context.Context, I, contract.Principal) (O, error)) func(context.Context, I, contract.Principal) (O, error) {
	return func(ctx context.Context, in I, p contract.Principal) (O, error) {
		var zero O
		tenant, err := deps.resolveTenant(ctx, intent, p)
		if err != nil {
			return zero, err
		}
		if f, ok := any(in).(tenantFilterer); ok {
			if named := f.tenantFilter(); named != nil && *named != tenant {
				return zero, permissionDenied("this request names a tenant other than the one the dashboard resolved for you")
			}
		}
		return fn(weave.WithTenant(ctx, tenant), in, p)
	}
}

// binding registers one intent with the dispatcher.
type binding struct {
	intent string
	bind   func(d *dispatcher.Dispatcher) error
}

func query[I, O any](deps Deps, intent string, fn func(context.Context, I, contract.Principal) (O, error)) binding {
	return binding{intent: intent, bind: func(d *dispatcher.Dispatcher) error {
		return dispatcher.RegisterQuery(d, ContributorName, intent, 1, withTenant(deps, intent, fn))
	}}
}

func command[I, O any](deps Deps, intent string, fn func(context.Context, I, contract.Principal) (O, error)) binding {
	return binding{intent: intent, bind: func(d *dispatcher.Dispatcher) error {
		return dispatcher.RegisterCommand(d, ContributorName, intent, 1, withTenant(deps, intent, fn))
	}}
}

// bindings lists every intent this package answers.
func bindings(deps Deps) []binding {
	return []binding{
		query(deps, "system.overview", systemOverviewHandler(deps)),
		query(deps, "system.components", systemComponentsHandler(deps)),
		query(deps, "collections.list", collectionsListHandler(deps)),
		query(deps, "collections.get", collectionsGetHandler(deps)),
		command(deps, "collections.create", collectionsCreateHandler(deps)),
		command(deps, "collections.update", collectionsUpdateHandler(deps)),
		command(deps, "collections.delete", collectionsDeleteHandler(deps)),
		command(deps, "collections.reindex", collectionsReindexHandler(deps)),
		query(deps, "documents.list", documentsListHandler(deps)),
		query(deps, "documents.get", documentsGetHandler(deps)),
		query(deps, "documents.spans", documentsSpansHandler(deps)),
		command(deps, "documents.ingest", documentsIngestHandler(deps)),
		command(deps, "documents.delete", documentsDeleteHandler(deps)),
		query(deps, "chunks.list", chunksListHandler(deps)),
		query(deps, "chunks.get", chunksGetHandler(deps)),
		command(deps, "retrieval.run", retrievalRunHandler(deps)),
		command(deps, "retrieval.assemble", retrievalAssembleHandler(deps)),
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
	return register(d, reg, wreg, manifestYAML, bindings(deps))
}

// register does Register's work on a given manifest and binding list. Every
// check runs before reg.Register, so a mistake never leaves a contributor
// registered with intents nothing answers.
func register(d *dispatcher.Dispatcher, reg contract.Registry, wreg contract.WardenRegistry, raw []byte, bs []binding) error {
	m, err := loader.Load(bytes.NewReader(raw), "weave/contract/manifest.yaml")
	if err != nil {
		return fmt.Errorf("weave/contract: load manifest: %w", err)
	}
	if err := loader.Validate(m, wreg); err != nil {
		return fmt.Errorf("weave/contract: validate manifest: %w", err)
	}
	declared := make(map[string]bool, len(m.Intents))
	for _, in := range m.Intents {
		declared[in.Name] = true
	}
	for _, b := range bs {
		if !declared[b.intent] {
			return fmt.Errorf("weave/contract: %s is bound but the manifest does not declare it", b.intent)
		}
	}
	if err := reg.Register(m); err != nil {
		return fmt.Errorf("weave/contract: register manifest: %w", err)
	}
	for _, b := range bs {
		if err := b.bind(d); err != nil {
			return fmt.Errorf("weave/contract: register %s: %w", b.intent, err)
		}
	}
	return nil
}
