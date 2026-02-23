# Weave

Composable RAG pipeline engine for Go. Ingest documents, chunk text, generate embeddings, store vectors, retrieve relevant context, and assemble LLM prompts — all in a single, pluggable library.

```go
import "github.com/xraph/weave"
```

## Features

- **Full RAG pipeline** — Load, chunk, embed, store, retrieve, and assemble in one engine
- **Pluggable components** — Swap loaders, chunkers, embedders, vector stores, and retrievers
- **Multi-tenancy** — Tenant-scoped data isolation via Forge scope context
- **Extension system** — Lifecycle hooks for auditing, metrics, tracing, and custom logic
- **HTTP API** — RESTful endpoints with OpenAPI metadata for collections, documents, and retrieval
- **Forge-native** — Runs as a Forge extension with DI, routing, and health checks
- **Multiple storage backends** — In-memory, PostgreSQL, SQLite for metadata; memory and pgvector for vectors

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/xraph/weave/engine"
    "github.com/xraph/weave/chunker"
    "github.com/xraph/weave/collection"
    "github.com/xraph/weave/embedder"
    "github.com/xraph/weave/store/memory"
    vsmemory "github.com/xraph/weave/vectorstore/memory"
)

func main() {
    ctx := context.Background()

    eng, err := engine.New(
        engine.WithStore(memory.New()),
        engine.WithVectorStore(vsmemory.New()),
        engine.WithEmbedder(embedder.NewOpenAI("text-embedding-3-small")),
        engine.WithChunker(chunker.NewRecursive()),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer eng.Stop(ctx)

    // Create a collection
    col := &collection.Collection{Name: "docs"}
    if err := eng.CreateCollection(ctx, col); err != nil {
        log.Fatal(err)
    }

    // Ingest a document
    result, err := eng.Ingest(ctx, &engine.IngestInput{
        CollectionID: col.ID,
        Title:        "Getting Started",
        Content:      "Weave is a composable RAG pipeline engine for Go...",
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Ingested: %s (%d chunks)\n", result.DocumentID, result.ChunkCount)

    // Retrieve relevant context
    chunks, err := eng.Retrieve(ctx, "How does Weave work?",
        engine.WithCollection(col.ID),
        engine.WithTopK(5),
    )
    if err != nil {
        log.Fatal(err)
    }

    for _, sc := range chunks {
        fmt.Printf("[%.2f] %s\n", sc.Score, sc.Chunk.Content[:80])
    }
}
```

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                        Engine                            │
│                                                          │
│  Ingest:   Loader → Chunker → Embedder → VectorStore    │
│  Retrieve: Query  → Embedder → Retriever → Results      │
│  Assemble: Results → TokenBudget → Citations → Context   │
│                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌─────────────────┐ │
│  │ Metadata    │  │ Vector Store │  │ Extension       │ │
│  │ Store       │  │              │  │ Registry        │ │
│  │ (Pg/SQLite/ │  │ (pgvector/   │  │ (audit, metrics │ │
│  │  memory)    │  │  memory)     │  │  tracing, etc.) │ │
│  └─────────────┘  └──────────────┘  └─────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

## Components

### Loaders

Extract text from various document formats.

| Loader | Description |
|--------|-------------|
| `loader.NewText()` | Plain text passthrough |
| `loader.NewMarkdown()` | Markdown with front-matter stripping |
| `loader.NewHTML()` | HTML to text conversion |
| `loader.NewCSV()` | CSV row-based loading |
| `loader.NewJSON()` | JSON document extraction |
| `loader.NewURL()` | Fetch and load from URLs |
| `loader.NewDirectory()` | Recursive directory loading |

### Chunkers

Split text into manageable chunks for embedding.

| Chunker | Description |
|---------|-------------|
| `chunker.NewRecursive()` | Recursive text splitting (default) |
| `chunker.NewSemantic()` | Semantic boundary-aware splitting |
| `chunker.NewSlidingWindow()` | Overlapping sliding window |
| `chunker.NewFixedSize()` | Fixed token-count chunks |
| `chunker.NewCode()` | Language-aware code splitting |

### Embedders

Generate vector embeddings from text.

| Embedder | Description |
|----------|-------------|
| `embedder.NewOpenAI(model)` | OpenAI embedding API |
| `embedder.NewLocal()` | Local embedding model |

### Vector Stores

Store and search vector embeddings.

| Store | Description |
|-------|-------------|
| `vectorstore/memory` | In-memory store for testing |
| `vectorstore/pgvector` | PostgreSQL with pgvector extension |

### Retrievers

Retrieve relevant chunks using different strategies.

| Retriever | Description |
|-----------|-------------|
| `retriever.NewSimilarity()` | Cosine similarity search |
| `retriever.NewMMR()` | Maximal Marginal Relevance (diversity) |
| `retriever.NewHybrid()` | Reciprocal Rank Fusion across strategies |

### Assembler

Build token-budgeted context strings with citation tracking for LLM prompts.

```go
import "github.com/xraph/weave/assembler"

asm := assembler.New(
    assembler.WithMaxTokens(4096),
)

result, err := asm.Assemble(ctx, retrievedResults)
// result.Context    — assembled context string
// result.Citations  — which chunks were included
// result.TotalTokens — token count used
// result.TruncatedCount — chunks dropped due to budget
```

## Metadata Stores

| Store | Use case |
|-------|----------|
| `store/memory` | Testing and development |
| `store/postgres` | Production with migrations |
| `store/sqlite` | Lightweight persistent storage |

## Configuration

```go
weave.Config{
    DefaultChunkSize:      512,                    // Target chunk size in tokens
    DefaultChunkOverlap:   50,                     // Overlap between chunks
    DefaultEmbeddingModel: "text-embedding-3-small", // Default embedding model
    DefaultChunkStrategy:  "recursive",            // Default chunking strategy
    DefaultTopK:           10,                     // Default retrieval result count
    ShutdownTimeout:       30 * time.Second,       // Graceful shutdown timeout
    IngestConcurrency:     4,                      // Concurrent batch ingestion
}
```

## HTTP API

Weave exposes a RESTful API under `/v1/` with OpenAPI metadata.

### Collections

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/collections` | Create a collection |
| `GET` | `/v1/collections` | List collections |
| `GET` | `/v1/collections/:collectionId` | Get collection details |
| `DELETE` | `/v1/collections/:collectionId` | Delete collection and all content |
| `GET` | `/v1/collections/:collectionId/stats` | Collection statistics |
| `POST` | `/v1/collections/:collectionId/reindex` | Re-embed all chunks |

### Documents

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/collections/:collectionId/documents` | Ingest a document |
| `POST` | `/v1/collections/:collectionId/documents/batch` | Batch ingest documents |
| `GET` | `/v1/collections/:collectionId/documents` | List documents in collection |
| `GET` | `/v1/documents/:documentId` | Get document details |
| `DELETE` | `/v1/documents/:documentId` | Delete document and chunks |

### Retrieval

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/collections/:collectionId/retrieve` | Semantic retrieval within a collection |
| `POST` | `/v1/search` | Hybrid search across collections |

## Forge Extension

Mount Weave into a Forge application:

```go
import (
    "github.com/xraph/forge"
    "github.com/xraph/weave/extension"
    "github.com/xraph/weave/engine"
    "github.com/xraph/weave/store/memory"
    vsmemory "github.com/xraph/weave/vectorstore/memory"
)

app := forge.New()
app.Register(extension.New(
    extension.WithEngineOptions(
        engine.WithStore(memory.New()),
        engine.WithVectorStore(vsmemory.New()),
        engine.WithEmbedder(embedder.NewOpenAI("text-embedding-3-small")),
        engine.WithChunker(chunker.NewRecursive()),
    ),
))
app.Start(ctx)
```

## Extension System

Extensions receive lifecycle hooks by implementing opt-in interfaces:

| Hook | Trigger |
|------|---------|
| `CollectionCreated` | Collection created |
| `CollectionDeleted` | Collection deleted |
| `IngestStarted` | Document ingestion begins |
| `IngestChunked` | Documents chunked |
| `IngestEmbedded` | Chunks embedded |
| `IngestCompleted` | Ingestion finished |
| `IngestFailed` | Ingestion failed |
| `RetrievalStarted` | Retrieval query begins |
| `RetrievalCompleted` | Retrieval finished |
| `RetrievalFailed` | Retrieval failed |
| `DocumentDeleted` | Document deleted |
| `ReindexStarted` | Collection reindex begins |
| `ReindexCompleted` | Collection reindex finished |
| `Shutdown` | Graceful shutdown |

### Built-in Extensions

- **`audit_hook`** — Bridges lifecycle events to Chronicle for audit trails
- **`observability`** — Exposes ingestion, retrieval, and store metrics

## Packages

| Package | Description |
|---------|-------------|
| `weave` | Core types, config, errors, context helpers |
| `engine` | Central RAG pipeline coordinator |
| `api` | HTTP handlers with OpenAPI metadata |
| `extension` | Forge extension adapter |
| `loader` | Document loaders (text, markdown, HTML, CSV, JSON, URL, directory) |
| `chunker` | Text chunkers (recursive, semantic, sliding window, fixed, code) |
| `embedder` | Embedding providers (OpenAI, local) |
| `vectorstore` | Vector store interface |
| `vectorstore/memory` | In-memory vector store |
| `vectorstore/pgvector` | PostgreSQL pgvector store |
| `retriever` | Retrieval strategies (similarity, MMR, hybrid) |
| `assembler` | Token-budgeted context assembly with citations |
| `collection` | Collection model and store interface |
| `document` | Document model and store interface |
| `chunk` | Chunk model and store interface |
| `store` | Composite metadata store interface |
| `store/memory` | In-memory metadata store |
| `store/postgres` | PostgreSQL metadata store with migrations |
| `store/sqlite` | SQLite metadata store |
| `ext` | Extension interfaces and registry |
| `id` | TypeID-based identifiers |
| `pipeline` | Composable pipeline steps |
| `middleware` | HTTP middleware (tenant, tracing, cache) |
| `audit_hook` | Chronicle audit bridge extension |
| `observability` | Metrics extension |

## Requirements

- Go 1.25+
- PostgreSQL with pgvector extension (for production vector store)
