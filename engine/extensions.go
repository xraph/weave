package engine

import "github.com/xraph/weave/plugins"

// ExtensionInfo names a registered extension and the lifecycle hooks it
// implements. Extensions expose only a name, so hooks are found by type
// assertion against each hook interface.
type ExtensionInfo struct {
	Name  string   `json:"name"`
	Hooks []string `json:"hooks"`
}

// DescribeExtensions lists registered extensions in registration order.
func (e *Engine) DescribeExtensions() []ExtensionInfo {
	if e.extensions == nil {
		return []ExtensionInfo{}
	}
	exts := e.extensions.Extensions()
	out := make([]ExtensionInfo, 0, len(exts))
	for _, x := range exts {
		out = append(out, ExtensionInfo{Name: x.Name(), Hooks: hooksOf(x)})
	}
	return out
}

func hooksOf(x plugins.Extension) []string {
	hooks := []string{}
	add := func(ok bool, name string) {
		if ok {
			hooks = append(hooks, name)
		}
	}
	_, ok := x.(plugins.CollectionCreated)
	add(ok, "collection_created")
	_, ok = x.(plugins.CollectionDeleted)
	add(ok, "collection_deleted")
	_, ok = x.(plugins.IngestStarted)
	add(ok, "ingest_started")
	_, ok = x.(plugins.IngestChunked)
	add(ok, "ingest_chunked")
	_, ok = x.(plugins.IngestEmbedded)
	add(ok, "ingest_embedded")
	_, ok = x.(plugins.IngestCompleted)
	add(ok, "ingest_completed")
	_, ok = x.(plugins.IngestFailed)
	add(ok, "ingest_failed")
	_, ok = x.(plugins.RetrievalStarted)
	add(ok, "retrieval_started")
	_, ok = x.(plugins.RetrievalCompleted)
	add(ok, "retrieval_completed")
	_, ok = x.(plugins.RetrievalFailed)
	add(ok, "retrieval_failed")
	_, ok = x.(plugins.DocumentDeleted)
	add(ok, "document_deleted")
	_, ok = x.(plugins.ReindexStarted)
	add(ok, "reindex_started")
	_, ok = x.(plugins.ReindexCompleted)
	add(ok, "reindex_completed")
	_, ok = x.(plugins.Shutdown)
	add(ok, "shutdown")
	return hooks
}
