package retriever

import (
	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/vectorstore"
)

// ChunkFromSearchResult builds the chunk a vector hit stands for. The vector
// entry's ID is the chunk ID ingest wrote, so it is carried through: without
// it nobody can tell which chunk, document or collection a hit came from.
// Everything else on the chunk comes from the metadata store, which the
// engine reads afterwards.
func ChunkFromSearchResult(sr vectorstore.SearchResult) *chunk.Chunk {
	chunkID, _ := id.ParseChunkID(sr.ID) //nolint:errcheck // a foreign ID leaves the chunk unidentified, which the engine reports
	return &chunk.Chunk{
		ID:       chunkID,
		Content:  sr.Content,
		Metadata: sr.Metadata,
	}
}
