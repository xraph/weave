// Package engine provides the central Weave RAG pipeline coordinator.
package engine

import (
	"log/slog"

	"github.com/xraph/weave"
	"github.com/xraph/weave/chunker"
	"github.com/xraph/weave/embedder"
	"github.com/xraph/weave/ext"
	"github.com/xraph/weave/loader"
	"github.com/xraph/weave/retriever"
	"github.com/xraph/weave/store"
	"github.com/xraph/weave/vectorstore"
)

// Option configures the Engine.
type Option func(*Engine) error

// WithStore sets the metadata store.
func WithStore(s store.Store) Option {
	return func(e *Engine) error {
		e.store = s
		return nil
	}
}

// WithVectorStore sets the vector store.
func WithVectorStore(vs vectorstore.VectorStore) Option {
	return func(e *Engine) error {
		e.vectorStore = vs
		return nil
	}
}

// WithEmbedder sets the embedder.
func WithEmbedder(emb embedder.Embedder) Option {
	return func(e *Engine) error {
		e.embedder = emb
		return nil
	}
}

// WithChunker sets the chunker.
func WithChunker(c chunker.Chunker) Option {
	return func(e *Engine) error {
		e.chunker = c
		return nil
	}
}

// WithLoader sets the default document loader.
func WithLoader(l loader.Loader) Option {
	return func(e *Engine) error {
		e.loader = l
		return nil
	}
}

// WithRetriever sets the retriever.
func WithRetriever(r retriever.Retriever) Option {
	return func(e *Engine) error {
		e.retriever = r
		return nil
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(e *Engine) error {
		e.logger = l
		return nil
	}
}

// WithExtension registers an extension with the engine.
func WithExtension(extension ext.Extension) Option {
	return func(e *Engine) error {
		e.pendingExts = append(e.pendingExts, extension)
		return nil
	}
}

// WithConfig sets the engine configuration.
func WithConfig(cfg weave.Config) Option {
	return func(e *Engine) error {
		e.config = cfg
		return nil
	}
}
