package dashboard

import (
	"context"

	"github.com/a-h/templ"

	"github.com/xraph/forge/extensions/dashboard/contributor"

	"github.com/xraph/weave/id"
)

// PluginWidget describes a widget contributed by a weave plugin.
type PluginWidget struct {
	ID         string
	Title      string
	Size       string // "sm", "md", "lg"
	RefreshSec int
	Render     func(ctx context.Context) templ.Component
}

// PluginPage describes an extra page route contributed by a plugin.
type PluginPage struct {
	Route  string
	Label  string
	Icon   string
	Render func(ctx context.Context) templ.Component
}

// Plugin is optionally implemented by weave plugins
// to contribute UI sections to the weave dashboard contributor.
type Plugin interface {
	DashboardWidgets(ctx context.Context) []PluginWidget
	DashboardSettingsPanel(ctx context.Context) templ.Component
	DashboardPages() []PluginPage
}

// CollectionDetailContributor is optionally implemented by plugins that want to
// contribute a section to the collection detail page.
type CollectionDetailContributor interface {
	DashboardCollectionDetailSection(ctx context.Context, colID id.CollectionID) templ.Component
}

// DocumentDetailContributor is optionally implemented by plugins that want to
// contribute a section to the document detail page.
type DocumentDetailContributor interface {
	DashboardDocumentDetailSection(ctx context.Context, docID id.DocumentID) templ.Component
}

// RetrievalResultContributor is optionally implemented by plugins that want to
// contribute a section to retrieval results.
type RetrievalResultContributor interface {
	DashboardRetrievalResultSection(ctx context.Context, query string) templ.Component
}

// PageContributor is an enhanced interface for plugins that need
// access to route parameters when rendering dashboard pages.
type PageContributor interface {
	DashboardNavItems() []contributor.NavItem
	DashboardRenderPage(ctx context.Context, route string, params contributor.Params) (templ.Component, error)
}
