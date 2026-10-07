package contract

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// assertNoVectors fails if any key anywhere in v's JSON names a vector or
// an embedding. Embeddings are large and never leave the server.
func assertNoVectors(t *testing.T, label string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	var walk func(path string, x any)
	walk = func(path string, x any) {
		switch val := x.(type) {
		case map[string]any:
			for k, child := range val {
				switch strings.ToLower(k) {
				case "vector", "vectors", "embedding", "embeddings":
					t.Errorf("%s: key %q at %s", label, k, path)
				}
				walk(path+"."+k, child)
			}
		case []any:
			for _, child := range val {
				walk(path+"[]", child)
			}
		}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("%s: decode: %v", label, err)
	}
	walk(label, decoded)
}

// TestNoResponseCarriesAVector calls every handler that returns data, on a
// populated engine, and walks each answer.
func TestNoResponseCarriesAVector(t *testing.T) {
	deps := newDeps(t, openMemory(t))
	ctx := context.Background()
	p := principal()
	col := mustCollection(t, deps, "kb")
	doc := mustIngest(t, ctx, deps, col.ID, "refunds", "refunds are issued within thirty days")
	mustIngest(t, ctx, deps, col.ID, "shipping", "shipping takes five working days")

	check := func(label string, v any, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		assertNoVectors(t, label, v)
	}

	ov, err := systemOverviewHandler(deps)(ctx, tenantInput{}, p)
	check("system.overview", ov, err)
	comps, err := systemComponentsHandler(deps)(ctx, struct{}{}, p)
	check("system.components", comps, err)
	cl, err := collectionsListHandler(deps)(ctx, collectionsListInput{}, p)
	check("collections.list", cl, err)
	cg, err := collectionsGetHandler(deps)(ctx, idInput{ID: col.ID.String()}, p)
	check("collections.get", cg, err)
	dl, err := documentsListHandler(deps)(ctx, documentsListInput{}, p)
	check("documents.list", dl, err)
	dg, err := documentsGetHandler(deps)(ctx, idInput{ID: doc.String()}, p)
	check("documents.get", dg, err)
	ds, err := documentsSpansHandler(deps)(ctx, idInput{ID: doc.String()}, p)
	check("documents.spans", ds, err)
	chl, err := chunksListHandler(deps)(ctx, chunksListInput{DocumentID: doc.String()}, p)
	check("chunks.list", chl, err)
	if len(chl.Items) == 0 {
		t.Fatal("chunks.list: no chunks to open")
	}
	chg, err := chunksGetHandler(deps)(ctx, idInput{ID: chl.Items[0].ID.String()}, p)
	check("chunks.get", chg, err)
	run, err := retrievalRunHandler(deps)(ctx, runInput{Query: "refunds"}, p)
	check("retrieval.run", run, err)

	// Echo the run's hits back, the way the console does.
	hits := echo(run.Result)
	if len(hits) == 0 {
		t.Fatal("retrieval.run: no hits to assemble")
	}
	ac, err := retrievalAssembleHandler(deps)(ctx, assembleInput{Hits: hits}, p)
	check("retrieval.assemble", ac, err)
}
