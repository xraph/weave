package extension

import (
	"testing"
	"time"

	"github.com/xraph/weave"
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
