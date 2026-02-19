// Package weave provides a composable RAG pipeline engine for Go.
//
// Weave handles the full document lifecycle: ingest documents, chunk text,
// generate embeddings, store vectors, retrieve relevant context, and
// assemble prompts. It is tenant-scoped, plugin-extensible, and Forge-native.
//
// The engine is built around pluggable interfaces:
//   - loader.Loader — extract text from various document formats
//   - chunker.Chunker — split text into manageable chunks
//   - embedder.Embedder — generate vector embeddings
//   - vectorstore.VectorStore — store and search vectors
//   - retriever.Retriever — retrieve relevant chunks
//
// All operations are tenant-scoped via forge.Scope for data isolation.
package weave
