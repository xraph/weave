// Package extension adapts the Weave engine as a Forge extension.
package extension

import (
	"log/slog"

	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/ext"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/vectorstore"
)

// Config configures the Weave Forge extension.
type Config struct {
	// DisableRoutes prevents HTTP route registration.
	DisableRoutes bool
	// DisableMigrate prevents auto-migration on start.
	DisableMigrate bool
	// BasePath is the URL prefix for all weave routes.
	BasePath string
}

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
	return func(e *Extension) {
		e.config = cfg
	}
}

// WithDisableRoutes disables HTTP route registration.
func WithDisableRoutes() ExtOption {
	return func(e *Extension) {
		e.config.DisableRoutes = true
	}
}

// WithDisableMigrate disables auto-migration on start.
func WithDisableMigrate() ExtOption {
	return func(e *Extension) {
		e.config.DisableMigrate = true
	}
}

// WithBasePath sets the URL prefix for all weave routes.
func WithBasePath(path string) ExtOption {
	return func(e *Extension) {
		e.config.BasePath = path
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) ExtOption {
	return func(e *Extension) {
		e.logger = l
	}
}
