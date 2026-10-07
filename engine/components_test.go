package engine_test

import (
	"slices"
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/loader"
	"github.com/xraph/weave/retriever"
)

func TestComponents(t *testing.T) {
	r := newRig(t,
		withOpt(engine.WithLoader(loader.NewMarkdownLoader())),
		func(r *testRig) engine.Option {
			return engine.WithRetriever(retriever.NewMMRRetriever(r.Vectors, r.Embed, 0.7))
		},
	)
	c := r.Engine.Components()

	if c.Retriever.Kind != "mmr" || c.Retriever.Params["lambda"] != "0.70" || !c.Retriever.Configured {
		t.Errorf("retriever: %+v", c.Retriever)
	}
	if c.Score != weave.ScoreMMR {
		t.Errorf("score: got %q, want %q", c.Score, weave.ScoreMMR)
	}
	if c.Chunker.Kind != "recursive" || c.VectorStore.Kind != "memory" || c.TenantFilter != "verified" {
		t.Errorf("chunker %q, vector store %q, tenant filter %q", c.Chunker.Kind, c.VectorStore.Kind, c.TenantFilter)
	}
	// The test embedder implements no Describe, so it reports as custom with
	// its Go type and its real dimensions.
	if c.Embedder.Kind != "custom" || c.Embedder.Type == "" || c.Embedder.Dimensions != r.Embed.dims {
		t.Errorf("embedder: %+v", c.Embedder)
	}
	// Content types are probed from the loader's own Supports, never a list.
	if !slices.Contains(c.Loader.ContentTypes, "text/markdown") || slices.Contains(c.Loader.ContentTypes, "text/html") {
		t.Errorf("loader content types: %v", c.Loader.ContentTypes)
	}

	bare := newTestEngine(t)
	bc := bare.Components()
	if bc.Retriever.Configured || bc.Loader.Configured {
		t.Errorf("bare engine: retriever %+v, loader %+v, want both unconfigured", bc.Retriever, bc.Loader)
	}
	if bc.Score != weave.ScoreCosine {
		t.Errorf("bare engine score: got %q, want cosine from the memory vector store", bc.Score)
	}
}
