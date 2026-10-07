package retriever_test

import (
	"testing"

	"github.com/xraph/weave"
	"github.com/xraph/weave/retriever"
	vsmemory "github.com/xraph/weave/vectorstore/memory"
)

func TestDescribe(t *testing.T) {
	vs := vsmemory.New()
	sim := retriever.NewSimilarityRetriever(vs, nil, nil)
	cases := []struct {
		name  string
		d     weave.Describer
		kind  string
		score weave.ScoreKind
	}{
		{"similarity inherits the vector store's score", sim, "similarity", weave.ScoreCosine},
		{"mmr", retriever.NewMMRRetriever(vs, nil, 0.4), "mmr", weave.ScoreMMR},
		{"hybrid", retriever.NewHybridRetriever(sim), "hybrid", weave.ScoreRRF},
		{"rerank", retriever.NewRerankerRetriever(sim, nil), "rerank", weave.ScoreRerank},
	}
	for _, tc := range cases {
		info := tc.d.Describe()
		if info.Kind != tc.kind || info.Score != tc.score {
			t.Errorf("%s: got kind %q score %q, want %q %q", tc.name, info.Kind, info.Score, tc.kind, tc.score)
		}
	}
	if got := retriever.NewMMRRetriever(vs, nil, 0.4).Describe().Params["lambda"]; got != "0.40" {
		t.Errorf("mmr lambda: got %q, want 0.40", got)
	}
	hy := retriever.NewHybridRetriever(sim).Describe()
	if hy.Params["k"] != "60" || len(hy.Children) != 1 || hy.Children[0].Kind != "similarity" {
		t.Errorf("hybrid: got %+v", hy)
	}
}
