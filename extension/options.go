// Package extension adapts the Weave engine as a Forge extension.
package extension

import (
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/ext"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/vectorstore"
)

// ExtOption configures the Weave Forge extension.
type ExtOption func(*Extension)

// WithStore sets the metadata store.
func WithStore(s store.Store) ExtOption {
	return func(e *Extension) {
		e.engineOpts = append(e.engineOpts, engine.WithStore(s))
	}
}

// WithVectorStore sets the vector store.
func WithVectorStore(vs vectorstore.VectorStore) ExtOption {
	return func(e *Extension) {
		e.engineOpts = append(e.engineOpts, engine.WithVectorStore(vs))
	}
}

// WithExtension registers a Weave extension (lifecycle hooks).
func WithExtension(x ext.Extension) ExtOption {
	return func(e *Extension) {
		e.engineOpts = append(e.engineOpts, engine.WithExtension(x))
	}
}

// WithEngineOption passes an engine option directly.
func WithEngineOption(opt engine.Option) ExtOption {
	return func(e *Extension) {
		e.engineOpts = append(e.engineOpts, opt)
	}
}

// WithConfig sets the extension configuration.
func WithConfig(cfg Config) ExtOption {
	return func(e *Extension) { e.config = cfg }
}

// WithDisableRoutes disables HTTP route registration.
func WithDisableRoutes() ExtOption {
	return func(e *Extension) { e.config.DisableRoutes = true }
}

// WithDisableMigrate disables auto-migration on start.
func WithDisableMigrate() ExtOption {
	return func(e *Extension) { e.config.DisableMigrate = true }
}

// WithBasePath sets the URL prefix for all weave routes.
func WithBasePath(path string) ExtOption {
	return func(e *Extension) { e.config.BasePath = path }
}

// WithRequireConfig requires config to be present in YAML files.
// If true and no config is found, Register returns an error.
func WithRequireConfig(require bool) ExtOption {
	return func(e *Extension) { e.config.RequireConfig = require }
}

// WithGroveDatabase sets the name of the grove.DB to resolve from the DI container.
// The extension will auto-construct the appropriate store backend (postgres/sqlite/mongo)
// based on the grove driver type. Pass an empty string to use the default (unnamed) grove.DB.
func WithGroveDatabase(name string) ExtOption {
	return func(e *Extension) {
		e.config.GroveDatabase = name
		e.useGrove = true
	}
}
