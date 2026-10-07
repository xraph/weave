package chunker

import "github.com/xraph/weave"

var (
	_ weave.Describer = (*FixedChunker)(nil)
	_ weave.Describer = (*RecursiveChunker)(nil)
	_ weave.Describer = (*SemanticChunker)(nil)
	_ weave.Describer = (*SlidingChunker)(nil)
	_ weave.Describer = (*CodeChunker)(nil)
)

// Describe reports the fixed-size chunker.
func (*FixedChunker) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "fixed"} }

// Describe reports the recursive chunker.
func (*RecursiveChunker) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "recursive"}
}

// Describe reports the semantic chunker. Its offsets are approximate.
func (*SemanticChunker) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "semantic", Params: map[string]string{"offsets": "approximate"}}
}

// Describe reports the sliding-window chunker.
func (*SlidingChunker) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "sliding"} }

// Describe reports the code chunker. Its offsets are approximate.
func (*CodeChunker) Describe() weave.ComponentInfo {
	return weave.ComponentInfo{Kind: "code", Params: map[string]string{"offsets": "approximate"}}
}
