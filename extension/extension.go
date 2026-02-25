// Package extension provides the Forge extension adapter for Weave.
//
// It implements the forge.Extension interface to integrate Weave
// into a Forge application with automatic dependency discovery,
// route registration, and lifecycle management.
//
// Configuration can be provided programmatically via ExtOption functions
// or via YAML configuration files under "extensions.weave" or "weave" keys.
package extension

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/xraph/forge"
	"github.com/xraph/grove"
	"github.com/xraph/vessel"

	"github.com/xraph/weave/api"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/store"
	mongostore "github.com/xraph/weave/store/mongo"
	pgstore "github.com/xraph/weave/store/postgres"
	sqlitestore "github.com/xraph/weave/store/sqlite"
)

// ExtensionName is the name registered with Forge.
const ExtensionName = "weave"

// ExtensionDescription is the human-readable description.
const ExtensionDescription = "Composable RAG pipeline engine for document ingestion, embedding, and retrieval"

// ExtensionVersion is the semantic version.
const ExtensionVersion = "0.1.0"

// Ensure Extension implements forge.Extension at compile time.
var _ forge.Extension = (*Extension)(nil)

// Extension adapts Weave as a Forge extension.
type Extension struct {
	*forge.BaseExtension

	config     Config
	eng        *engine.Engine
	apiHandler *api.API
	engineOpts []engine.Option
	useGrove   bool
}

// New creates a Weave Forge extension with the given options.
func New(opts ...ExtOption) *Extension {
	e := &Extension{
		BaseExtension: forge.NewBaseExtension(ExtensionName, ExtensionVersion, ExtensionDescription),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Engine returns the underlying Weave engine.
// This is nil until Register is called.
func (e *Extension) Engine() *engine.Engine { return e.eng }

// API returns the API handler.
func (e *Extension) API() *api.API { return e.apiHandler }

// Register implements [forge.Extension]. It loads configuration,
// initializes the engine, and registers it in the DI container.
func (e *Extension) Register(fapp forge.App) error {
	if err := e.BaseExtension.Register(fapp); err != nil {
		return err
	}

	if err := e.loadConfiguration(); err != nil {
		return err
	}

	// Resolve store from grove DI if configured.
	if e.useGrove {
		groveDB, err := e.resolveGroveDB(fapp)
		if err != nil {
			return fmt.Errorf("weave: %w", err)
		}
		s, err := e.buildStoreFromGroveDB(groveDB)
		if err != nil {
			return err
		}
		e.engineOpts = append(e.engineOpts, engine.WithStore(s))
	}

	eng, err := engine.New(e.engineOpts...)
	if err != nil {
		return fmt.Errorf("weave: create engine: %w", err)
	}
	e.eng = eng

	// Create the API handler.
	e.apiHandler = api.New(e.eng, fapp.Router())

	// Register HTTP routes unless disabled.
	if !e.config.DisableRoutes {
		e.apiHandler.RegisterRoutes(fapp.Router())
	}

	// Register the engine in the DI container so other extensions can use it.
	return vessel.Provide(fapp.Container(), func() (*engine.Engine, error) {
		return e.eng, nil
	})
}

// Start begins the Weave engine and runs auto-migration if enabled.
func (e *Extension) Start(ctx context.Context) error {
	if e.eng == nil {
		return errors.New("weave: extension not initialized")
	}

	// Run migrations unless disabled.
	if !e.config.DisableMigrate {
		s := e.eng.Store()
		if s != nil {
			if err := s.Migrate(ctx); err != nil {
				return fmt.Errorf("weave: migration failed: %w", err)
			}
		}
	}

	if err := e.eng.Start(ctx); err != nil {
		return err
	}

	e.MarkStarted()
	return nil
}

// Stop gracefully shuts down the Weave engine.
func (e *Extension) Stop(ctx context.Context) error {
	if e.eng != nil {
		if err := e.eng.Stop(ctx); err != nil {
			e.MarkStopped()
			return err
		}
	}
	e.MarkStopped()
	return nil
}

// Health implements [forge.Extension].
func (e *Extension) Health(ctx context.Context) error {
	if e.eng == nil {
		return errors.New("weave: extension not initialized")
	}

	s := e.eng.Store()
	if s == nil {
		return errors.New("weave: no store configured")
	}

	return s.Ping(ctx)
}

// Handler returns the HTTP handler for all API routes.
// Convenience for standalone use outside Forge.
func (e *Extension) Handler() http.Handler {
	if e.apiHandler == nil {
		return http.NotFoundHandler()
	}
	return e.apiHandler.Handler()
}

// RegisterRoutes registers all Weave API routes into a Forge router.
func (e *Extension) RegisterRoutes(router forge.Router) {
	if e.apiHandler != nil {
		e.apiHandler.RegisterRoutes(router)
	}
}

// --- Config Loading (mirrors grove extension pattern) ---

// loadConfiguration loads config from YAML files or programmatic sources.
func (e *Extension) loadConfiguration() error {
	programmaticConfig := e.config

	// Try loading from config file.
	fileConfig, configLoaded := e.tryLoadFromConfigFile()

	if !configLoaded {
		if programmaticConfig.RequireConfig {
			return errors.New("weave: configuration is required but not found in config files; " +
				"ensure 'extensions.weave' or 'weave' key exists in your config")
		}

		// Use programmatic config merged with defaults.
		e.config = e.mergeWithDefaults(programmaticConfig)
	} else {
		// Config loaded from YAML -- merge with programmatic options.
		e.config = e.mergeConfigurations(fileConfig, programmaticConfig)
	}

	// Enable grove resolution if YAML config specifies a grove database.
	if e.config.GroveDatabase != "" {
		e.useGrove = true
	}

	e.Logger().Debug("weave: configuration loaded",
		forge.F("disable_routes", e.config.DisableRoutes),
		forge.F("disable_migrate", e.config.DisableMigrate),
		forge.F("base_path", e.config.BasePath),
		forge.F("grove_database", e.config.GroveDatabase),
		forge.F("default_chunk_size", e.config.DefaultChunkSize),
		forge.F("default_embedding_model", e.config.DefaultEmbeddingModel),
		forge.F("ingest_concurrency", e.config.IngestConcurrency),
	)

	return nil
}

// tryLoadFromConfigFile attempts to load config from YAML files.
func (e *Extension) tryLoadFromConfigFile() (Config, bool) {
	cm := e.App().Config()
	var cfg Config

	// Try "extensions.weave" first (namespaced pattern).
	if cm.IsSet("extensions.weave") {
		if err := cm.Bind("extensions.weave", &cfg); err == nil {
			e.Logger().Debug("weave: loaded config from file",
				forge.F("key", "extensions.weave"),
			)
			return cfg, true
		}
		e.Logger().Warn("weave: failed to bind extensions.weave config",
			forge.F("error", "bind failed"),
		)
	}

	// Try legacy "weave" key.
	if cm.IsSet("weave") {
		if err := cm.Bind("weave", &cfg); err == nil {
			e.Logger().Debug("weave: loaded config from file",
				forge.F("key", "weave"),
			)
			return cfg, true
		}
		e.Logger().Warn("weave: failed to bind weave config",
			forge.F("error", "bind failed"),
		)
	}

	return Config{}, false
}

// mergeWithDefaults fills zero-valued fields with defaults.
func (e *Extension) mergeWithDefaults(cfg Config) Config {
	defaults := DefaultConfig()
	if cfg.DefaultChunkSize == 0 {
		cfg.DefaultChunkSize = defaults.DefaultChunkSize
	}
	if cfg.DefaultChunkOverlap == 0 {
		cfg.DefaultChunkOverlap = defaults.DefaultChunkOverlap
	}
	if cfg.DefaultEmbeddingModel == "" {
		cfg.DefaultEmbeddingModel = defaults.DefaultEmbeddingModel
	}
	if cfg.DefaultChunkStrategy == "" {
		cfg.DefaultChunkStrategy = defaults.DefaultChunkStrategy
	}
	if cfg.DefaultTopK == 0 {
		cfg.DefaultTopK = defaults.DefaultTopK
	}
	if cfg.ShutdownTimeout == 0 {
		cfg.ShutdownTimeout = defaults.ShutdownTimeout
	}
	if cfg.IngestConcurrency == 0 {
		cfg.IngestConcurrency = defaults.IngestConcurrency
	}
	return cfg
}

// mergeConfigurations merges YAML config with programmatic options.
// YAML config takes precedence for most fields; programmatic bool flags fill gaps.
func (e *Extension) mergeConfigurations(yamlConfig, programmaticConfig Config) Config {
	// Programmatic bool flags override when true.
	if programmaticConfig.DisableRoutes {
		yamlConfig.DisableRoutes = true
	}
	if programmaticConfig.DisableMigrate {
		yamlConfig.DisableMigrate = true
	}

	// String fields: YAML takes precedence.
	if yamlConfig.BasePath == "" && programmaticConfig.BasePath != "" {
		yamlConfig.BasePath = programmaticConfig.BasePath
	}
	if yamlConfig.GroveDatabase == "" && programmaticConfig.GroveDatabase != "" {
		yamlConfig.GroveDatabase = programmaticConfig.GroveDatabase
	}
	if yamlConfig.DefaultEmbeddingModel == "" && programmaticConfig.DefaultEmbeddingModel != "" {
		yamlConfig.DefaultEmbeddingModel = programmaticConfig.DefaultEmbeddingModel
	}
	if yamlConfig.DefaultChunkStrategy == "" && programmaticConfig.DefaultChunkStrategy != "" {
		yamlConfig.DefaultChunkStrategy = programmaticConfig.DefaultChunkStrategy
	}

	// Int/Duration fields: YAML takes precedence, programmatic fills gaps.
	if yamlConfig.DefaultChunkSize == 0 && programmaticConfig.DefaultChunkSize != 0 {
		yamlConfig.DefaultChunkSize = programmaticConfig.DefaultChunkSize
	}
	if yamlConfig.DefaultChunkOverlap == 0 && programmaticConfig.DefaultChunkOverlap != 0 {
		yamlConfig.DefaultChunkOverlap = programmaticConfig.DefaultChunkOverlap
	}
	if yamlConfig.DefaultTopK == 0 && programmaticConfig.DefaultTopK != 0 {
		yamlConfig.DefaultTopK = programmaticConfig.DefaultTopK
	}
	if yamlConfig.ShutdownTimeout == 0 && programmaticConfig.ShutdownTimeout != 0 {
		yamlConfig.ShutdownTimeout = programmaticConfig.ShutdownTimeout
	}
	if yamlConfig.IngestConcurrency == 0 && programmaticConfig.IngestConcurrency != 0 {
		yamlConfig.IngestConcurrency = programmaticConfig.IngestConcurrency
	}

	// Fill remaining zeros with defaults.
	return e.mergeWithDefaults(yamlConfig)
}

// resolveGroveDB resolves a *grove.DB from the DI container.
// If GroveDatabase is set, it looks up the named DB; otherwise it uses the default.
func (e *Extension) resolveGroveDB(fapp forge.App) (*grove.DB, error) {
	if e.config.GroveDatabase != "" {
		db, err := vessel.InjectNamed[*grove.DB](fapp.Container(), e.config.GroveDatabase)
		if err != nil {
			return nil, fmt.Errorf("grove database %q not found in container: %w", e.config.GroveDatabase, err)
		}
		return db, nil
	}
	db, err := vessel.Inject[*grove.DB](fapp.Container())
	if err != nil {
		return nil, fmt.Errorf("default grove database not found in container: %w", err)
	}
	return db, nil
}

// buildStoreFromGroveDB constructs the appropriate store backend
// based on the grove driver type (pg, sqlite, mongo).
func (e *Extension) buildStoreFromGroveDB(db *grove.DB) (store.Store, error) {
	driverName := db.Driver().Name()
	switch driverName {
	case "pg":
		return pgstore.New(db), nil
	case "sqlite":
		return sqlitestore.New(db), nil
	case "mongo":
		return mongostore.New(db), nil
	default:
		return nil, fmt.Errorf("weave: unsupported grove driver %q", driverName)
	}
}
