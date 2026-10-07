package extension

import (
	"strings"
	"testing"
	"time"

	"github.com/xraph/forge"

	"github.com/xraph/weave"
	"github.com/xraph/weave/store/memory"
)

func TestEngineConfig_DefaultsWhenUnset(t *testing.T) {
	if got := (Config{}).engineConfig(); got != weave.DefaultConfig() {
		t.Errorf("zero Config: %+v, want weave.DefaultConfig()", got)
	}
}

// Before this, the YAML defaults never reached the engine.
func TestEngineConfig_PassesSettingsThrough(t *testing.T) {
	got := Config{
		DefaultChunkSize: 300, DefaultChunkOverlap: 30, DefaultEmbeddingModel: "m",
		DefaultChunkStrategy: "fixed", DefaultTopK: 7, ShutdownTimeout: 5 * time.Second, IngestConcurrency: 2,
	}.engineConfig()
	want := weave.Config{
		DefaultChunkSize: 300, DefaultChunkOverlap: 30, DefaultEmbeddingModel: "m",
		DefaultChunkStrategy: "fixed", DefaultTopK: 7, ShutdownTimeout: 5 * time.Second, IngestConcurrency: 2,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// An overlap at or above the chunk size hangs the fixed chunker and panics
// the recursive one, so Register refuses it, naming the effective values
// after the defaults are applied. The keys go through the extension's real
// config path, the app's ConfigManager under extensions.weave.
func TestRegister_RefusesOverlapAtOrAboveChunkSize(t *testing.T) {
	cases := []struct {
		name string
		keys map[string]any
		want string // "" means Register succeeds
	}{
		{"size 32 with the default overlap", map[string]any{"default_chunk_size": 32}, "default_chunk_overlap 50 must be smaller than default_chunk_size 32"},
		{"overlap equal to size", map[string]any{"default_chunk_size": 100, "default_chunk_overlap": 100}, "default_chunk_overlap 100 must be smaller than default_chunk_size 100"},
		{"overlap above the default size", map[string]any{"default_chunk_overlap": 600}, "default_chunk_overlap 600 must be smaller than default_chunk_size 512"},
		{"overlap one below size", map[string]any{"default_chunk_size": 100, "default_chunk_overlap": 99}, ""},
		{"defaults", map[string]any{"base_path": "/weave"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cm := forge.NewManager()
			cm.Set("extensions.weave", tc.keys)
			ext := New(WithStore(memory.New()), WithDisableRoutes())
			err := ext.Register(forge.New(forge.WithAppName("t"), forge.WithAppConfigManager(cm)))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Register: %v", err)
			case tc.want == "" && ext.Engine().Config().DefaultChunkSize != ext.config.DefaultChunkSize:
				t.Fatalf("the YAML never reached the engine: engine %+v, extension %+v", ext.Engine().Config(), ext.config)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("Register: %v, want an error containing %q", err, tc.want)
			case tc.want != "" && ext.Engine() != nil:
				t.Error("a refused config still left an engine behind")
			}
		})
	}
}

// The same check holds for a config set in code.
func TestRegister_RefusesOverlapSetInCode(t *testing.T) {
	ext := New(WithConfig(Config{DefaultChunkSize: 32}), WithStore(memory.New()), WithDisableRoutes())
	err := ext.Register(forge.New(forge.WithAppName("t")))
	if err == nil || !strings.Contains(err.Error(), "default_chunk_overlap 50 must be smaller than default_chunk_size 32") {
		t.Fatalf("Register: %v", err)
	}
}
