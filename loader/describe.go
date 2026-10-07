package loader

import "github.com/xraph/weave"

var (
	_ weave.Describer = (*TextLoader)(nil)
	_ weave.Describer = (*MarkdownLoader)(nil)
	_ weave.Describer = (*HTMLLoader)(nil)
	_ weave.Describer = (*CSVLoader)(nil)
	_ weave.Describer = (*JSONLoader)(nil)
	_ weave.Describer = (*URLLoader)(nil)
	_ weave.Describer = (*DirectoryLoader)(nil)
)

// Describe reports the plain-text loader.
func (*TextLoader) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "text"} }

// Describe reports the Markdown loader.
func (*MarkdownLoader) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "markdown"} }

// Describe reports the HTML loader.
func (*HTMLLoader) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "html"} }

// Describe reports the CSV loader.
func (*CSVLoader) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "csv"} }

// Describe reports the JSON loader.
func (*JSONLoader) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "json"} }

// Describe reports the URL loader.
func (*URLLoader) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "url"} }

// Describe reports the directory loader. Its Supports never matches a
// content type, so the Pipeline page lists none for it.
func (*DirectoryLoader) Describe() weave.ComponentInfo { return weave.ComponentInfo{Kind: "directory"} }
