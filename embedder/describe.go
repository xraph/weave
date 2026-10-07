package embedder

import "github.com/xraph/weave"

var (
	_ weave.Describer = (*OpenAIEmbedder)(nil)
	_ weave.Describer = (*LocalEmbedder)(nil)
)

// Describe reports the OpenAI embedder and its model.
func (e *OpenAIEmbedder) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "openai", Params: map[string]string{"model": e.model}}
}

// Describe reports the local embedder, which is not implemented yet: every
// Embed call returns ErrNotImplemented.
func (*LocalEmbedder) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "local", Params: map[string]string{"implemented": "false"}}
}
