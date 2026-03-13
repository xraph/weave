package dashboard

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/a-h/templ"

	"github.com/xraph/forge/extensions/dashboard/contributor"

	"github.com/xraph/weave/chunk"
	"github.com/xraph/weave/collection"
	"github.com/xraph/weave/dashboard/components"
	"github.com/xraph/weave/dashboard/pages"
	"github.com/xraph/weave/dashboard/settings"
	"github.com/xraph/weave/dashboard/widgets"
	"github.com/xraph/weave/document"
	"github.com/xraph/weave/engine"
	"github.com/xraph/weave/id"
	"github.com/xraph/weave/plugins"
	"github.com/xraph/weave/store"
)

var _ contributor.LocalContributor = (*Contributor)(nil)

// Contributor implements the dashboard LocalContributor interface for the
// weave extension.
type Contributor struct {
	manifest *contributor.Manifest
	engine   *engine.Engine
	exts     []plugins.Extension
}

// New creates a new weave dashboard contributor.
func New(manifest *contributor.Manifest, eng *engine.Engine, exts []plugins.Extension) *Contributor {
	return &Contributor{
		manifest: manifest,
		engine:   eng,
		exts:     exts,
	}
}

// Manifest returns the contributor manifest.
func (c *Contributor) Manifest() *contributor.Manifest { return c.manifest }

// RenderPage renders a page for the given route.
func (c *Contributor) RenderPage(ctx context.Context, route string, params contributor.Params) (templ.Component, error) {
	if c.engine == nil {
		return components.EmptyState("alert-circle", "Engine not initialized", "The Weave engine is not available. Please check extension configuration."), nil
	}
	s := c.engine.Store()
	if s == nil {
		return components.EmptyState("database", "No store configured", "The Weave dashboard requires a database store. Please configure a Grove driver or provide a store via engine options."), nil
	}
	comp, err := c.renderPageRoute(ctx, route, s, params)
	if err != nil {
		return nil, err
	}
	pagesBase := params.BasePath + "/ext/" + c.manifest.Name + "/pages"
	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		return components.PathRewriter(pagesBase).Render(templ.WithChildren(tCtx, comp), w)
	}), nil
}

func (c *Contributor) renderPageRoute(ctx context.Context, pageRoute string, s store.Store, params contributor.Params) (templ.Component, error) {
	pageRoute = strings.TrimRight(pageRoute, "/")
	if pageRoute == "" {
		pageRoute = "/"
	}

	// Check plugin-contributed pages first.
	for _, p := range c.exts {
		if dpc, ok := p.(PageContributor); ok {
			if comp, err := dpc.DashboardRenderPage(ctx, pageRoute, params); err == nil && comp != nil {
				return comp, nil
			}
		}
	}

	for _, dp := range c.dashboardPlugins() {
		for _, pp := range dp.DashboardPages() {
			if pp.Route == pageRoute {
				return pp.Render(ctx), nil
			}
		}
	}

	switch pageRoute {
	case "/":
		return c.renderOverview(ctx, s)
	case "/collections":
		return c.renderCollections(ctx, s, params)
	case "/collections/detail":
		return c.renderCollectionDetail(ctx, s, params)
	case "/collections/create":
		return c.renderCollectionForm(ctx, s, params)
	case "/collections/edit":
		return c.renderCollectionForm(ctx, s, params)
	case "/documents":
		return c.renderDocuments(ctx, s, params)
	case "/documents/detail":
		return c.renderDocumentDetail(ctx, s, params)
	case "/chunks":
		return c.renderChunks(ctx, s, params)
	case "/chunks/detail":
		return c.renderChunkDetail(ctx, s, params)
	case "/chunks/browser":
		return c.renderChunks(ctx, s, params)
	case "/retrieval":
		return c.renderRetrieval(ctx, s, params)
	case "/pipeline":
		return c.renderPipeline(ctx)
	case "/loaders":
		return c.renderLoaders(ctx)
	case "/extensions":
		return c.renderExtensions(ctx)
	default:
		return components.EmptyState("alert-circle", "Page not found", "The requested page '"+pageRoute+"' does not exist in the Weave dashboard."), nil
	}
}

// RenderWidget renders a widget by ID.
func (c *Contributor) RenderWidget(ctx context.Context, widgetID string) (templ.Component, error) {
	if c.engine == nil {
		return nil, contributor.ErrWidgetNotFound
	}
	s := c.engine.Store()
	if s == nil {
		return nil, contributor.ErrWidgetNotFound
	}

	for _, dp := range c.dashboardPlugins() {
		for _, w := range dp.DashboardWidgets(ctx) {
			if w.ID == widgetID {
				return w.Render(ctx), nil
			}
		}
	}

	switch widgetID {
	case "weave-stats":
		return c.renderStatsWidget(ctx, s)
	case "weave-recent-ingestions":
		return c.renderRecentIngestionsWidget(ctx, s)
	case "weave-pipeline-health":
		return c.renderPipelineHealthWidget(ctx, s)
	default:
		return nil, contributor.ErrWidgetNotFound
	}
}

// RenderSettings renders a settings panel by ID.
func (c *Contributor) RenderSettings(ctx context.Context, settingID string) (templ.Component, error) {
	pluginSettings := c.collectPluginSettings(ctx)

	switch settingID {
	case "weave-config":
		return c.renderSettings(ctx, pluginSettings)
	default:
		return nil, contributor.ErrSettingNotFound
	}
}

// --- Page Renderers ---

func (c *Contributor) renderOverview(ctx context.Context, s store.Store) (templ.Component, error) {
	ec := fetchEntityCounts(ctx, s)
	counts := pages.EntityCounts{
		Collections:    ec.Collections,
		Documents:      ec.Documents,
		DocsReady:      ec.DocsReady,
		DocsProcessing: ec.DocsProcessing,
		DocsFailed:     ec.DocsFailed,
		DocsPending:    ec.DocsPending,
		Chunks:         ec.Chunks,
	}
	docs, _ := fetchRecentDocuments(ctx, s, 10) //nolint:errcheck // best-effort
	colNames := buildCollectionNameMap(ctx, s)
	pluginSections := c.collectPluginSections(ctx)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.OverviewPage(counts, docs, colNames).Render(childCtx, w)
	}), nil
}

func (c *Contributor) renderCollections(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	search := params.QueryParams["search"]
	limit := parseIntParam(params.QueryParams, "limit", 20)
	offset := parseIntParam(params.QueryParams, "offset", 0)
	items, total, err := fetchCollectionsPaginated(ctx, s, search, limit, offset)
	if err != nil {
		items = nil
		total = 0
	}
	pg := NewPaginationMeta(total, limit, offset)
	return pages.CollectionsPage(items, search, pg), nil
}

func (c *Contributor) renderCollectionDetail(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	idStr := params.QueryParams["id"]
	if idStr == "" {
		return nil, contributor.ErrPageNotFound
	}
	colID, err := id.ParseCollectionID(idStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}
	col, err := s.GetCollection(ctx, colID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve collection: %w", err)
	}
	stats, _ := c.engine.CollectionStats(ctx, colID)                                           //nolint:errcheck // best-effort
	recentDocs, _ := s.ListDocuments(ctx, &document.ListFilter{CollectionID: colID, Limit: 5}) //nolint:errcheck // best-effort
	pluginSections := c.collectCollectionDetailSections(ctx, colID)

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.CollectionDetailPage(col, stats, recentDocs).Render(childCtx, w)
	}), nil
}

func (c *Contributor) renderCollectionForm(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	cfg := c.engine.Config()
	idStr := params.QueryParams["id"]
	if idStr != "" {
		colID, err := id.ParseCollectionID(idStr)
		if err != nil {
			return nil, contributor.ErrPageNotFound
		}
		col, err := s.GetCollection(ctx, colID)
		if err != nil {
			return nil, fmt.Errorf("dashboard: resolve collection for edit: %w", err)
		}
		return pages.CollectionFormPage(col, cfg), nil
	}
	return pages.CollectionFormPage(nil, cfg), nil
}

func (c *Contributor) renderDocuments(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	search := params.QueryParams["search"]
	stateFilter := params.QueryParams["state"]
	colFilter := params.QueryParams["collection"]
	limit := parseIntParam(params.QueryParams, "limit", 20)
	offset := parseIntParam(params.QueryParams, "offset", 0)
	items, total, err := fetchDocumentsPaginated(ctx, s, colFilter, stateFilter, search, limit, offset)
	if err != nil {
		items = nil
		total = 0
	}
	collections, _ := s.ListCollections(ctx, &collection.ListFilter{}) //nolint:errcheck // best-effort
	colNames := buildCollectionNameMap(ctx, s)
	pg := NewPaginationMeta(total, limit, offset)
	return pages.DocumentsPage(items, collections, colNames, search, stateFilter, colFilter, pg), nil
}

func (c *Contributor) renderDocumentDetail(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	idStr := params.QueryParams["id"]
	if idStr == "" {
		return nil, contributor.ErrPageNotFound
	}
	docID, err := id.ParseDocumentID(idStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}
	doc, err := s.GetDocument(ctx, docID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve document: %w", err)
	}
	chunks, _ := s.ListChunksByDocument(ctx, docID)                            //nolint:errcheck // best-effort
	chunkCount, _ := s.CountChunks(ctx, &chunk.CountFilter{DocumentID: docID}) //nolint:errcheck // best-effort
	colNames := buildCollectionNameMap(ctx, s)
	pluginSections := c.collectDocumentDetailSections(ctx, docID)

	// Limit chunks preview to 5
	previewChunks := chunks
	if len(previewChunks) > 5 {
		previewChunks = previewChunks[:5]
	}

	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSections))
		return pages.DocumentDetailPage(doc, previewChunks, chunkCount, colNames).Render(childCtx, w)
	}), nil
}

func (c *Contributor) renderChunks(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	docIDStr := params.QueryParams["doc"]
	if docIDStr == "" {
		return components.EmptyState("puzzle", "Select a document", "Navigate to a document detail page and click 'View Chunks' to browse chunks."), nil
	}
	docID, err := id.ParseDocumentID(docIDStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}
	chunks, _ := s.ListChunksByDocument(ctx, docID) //nolint:errcheck // best-effort
	doc, _ := s.GetDocument(ctx, docID)             //nolint:errcheck // best-effort
	return pages.ChunksPage(chunks, doc), nil
}

func (c *Contributor) renderChunkDetail(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	idStr := params.QueryParams["id"]
	if idStr == "" {
		return nil, contributor.ErrPageNotFound
	}
	chunkID, err := id.ParseChunkID(idStr)
	if err != nil {
		return nil, contributor.ErrPageNotFound
	}
	ch, err := s.GetChunk(ctx, chunkID)
	if err != nil {
		return nil, fmt.Errorf("dashboard: resolve chunk: %w", err)
	}
	return pages.ChunkDetailPage(ch), nil
}

func (c *Contributor) renderRetrieval(ctx context.Context, s store.Store, params contributor.Params) (templ.Component, error) {
	collections, _ := s.ListCollections(ctx, &collection.ListFilter{}) //nolint:errcheck // best-effort

	// Check if there's a query to execute
	query := params.QueryParams["q"]
	var results []engine.ScoredChunk
	if query != "" {
		colIDStr := params.QueryParams["collection"]
		strategy := params.QueryParams["strategy"]
		topK := parseIntParam(params.QueryParams, "top_k", 10)
		minScore := parseFloat64Param(params.QueryParams, "min_score", 0.0)

		opts := []engine.RetrieveOption{
			engine.WithTopK(topK),
			engine.WithMinScore(minScore),
		}
		if strategy != "" {
			opts = append(opts, engine.WithStrategy(strategy))
		}
		if colIDStr != "" {
			colID, err := id.ParseCollectionID(colIDStr)
			if err == nil {
				opts = append(opts, engine.WithCollection(colID))
			}
		}
		results, _ = c.engine.Retrieve(ctx, query, opts...) //nolint:errcheck // best-effort
	}

	colNames := buildCollectionNameMap(ctx, s)
	return pages.RetrievalPage(collections, query, results, colNames, params.QueryParams), nil
}

func (c *Contributor) renderPipeline(_ context.Context) (templ.Component, error) {
	cfg := c.engine.Config()
	status := fetchPipelineStatus(c.engine)
	info := pages.PipelineInfo{
		HasLoader:      status.HasLoader,
		HasChunker:     status.HasChunker,
		HasEmbedder:    status.HasEmbedder,
		HasVectorStore: status.HasVectorStore,
		HasRetriever:   status.HasRetriever,
	}
	return pages.PipelinePage(cfg, info), nil
}

func (c *Contributor) renderLoaders(_ context.Context) (templ.Component, error) {
	return pages.LoadersPage(), nil
}

func (c *Contributor) renderExtensions(_ context.Context) (templ.Component, error) {
	registry := c.engine.Extensions()
	var extNames []string
	if registry != nil {
		for _, ext := range registry.Extensions() {
			extNames = append(extNames, ext.Name())
		}
	}
	return pages.ExtensionsPage(extNames), nil
}

// --- Widget Renderers ---

func (c *Contributor) renderStatsWidget(ctx context.Context, s store.Store) (templ.Component, error) {
	ec := fetchEntityCounts(ctx, s)
	counts := widgets.EntityCounts{
		Collections:    ec.Collections,
		Documents:      ec.Documents,
		DocsReady:      ec.DocsReady,
		DocsProcessing: ec.DocsProcessing,
		DocsFailed:     ec.DocsFailed,
		DocsPending:    ec.DocsPending,
		Chunks:         ec.Chunks,
	}
	return widgets.StatsWidget(counts), nil
}

func (c *Contributor) renderRecentIngestionsWidget(ctx context.Context, s store.Store) (templ.Component, error) {
	docs, _ := fetchRecentDocuments(ctx, s, 10) //nolint:errcheck // best-effort
	colNames := buildCollectionNameMap(ctx, s)
	return widgets.RecentIngestionsWidget(docs, colNames), nil
}

func (c *Contributor) renderPipelineHealthWidget(ctx context.Context, s store.Store) (templ.Component, error) {
	ec := fetchEntityCounts(ctx, s)
	counts := widgets.EntityCounts{
		Collections:    ec.Collections,
		Documents:      ec.Documents,
		DocsReady:      ec.DocsReady,
		DocsProcessing: ec.DocsProcessing,
		DocsFailed:     ec.DocsFailed,
		DocsPending:    ec.DocsPending,
		Chunks:         ec.Chunks,
	}
	ps := fetchPipelineStatus(c.engine)
	status := widgets.PipelineStatus{
		HasLoader:      ps.HasLoader,
		HasChunker:     ps.HasChunker,
		HasEmbedder:    ps.HasEmbedder,
		HasVectorStore: ps.HasVectorStore,
		HasRetriever:   ps.HasRetriever,
	}
	return widgets.PipelineHealthWidget(counts, status), nil
}

// --- Settings Renderer ---

func (c *Contributor) renderSettings(_ context.Context, pluginSettings []templ.Component) (templ.Component, error) {
	if c.engine == nil {
		return nil, contributor.ErrSettingNotFound
	}
	cfg := c.engine.Config()
	extNames := make([]string, 0)
	if reg := c.engine.Extensions(); reg != nil {
		for _, ext := range reg.Extensions() {
			extNames = append(extNames, ext.Name())
		}
	}
	return templ.ComponentFunc(func(tCtx context.Context, w io.Writer) error {
		childCtx := templ.WithChildren(tCtx, components.PluginSections(pluginSettings))
		return settings.ConfigPanel(cfg, extNames).Render(childCtx, w)
	}), nil
}

// --- Plugin Helpers ---

func (c *Contributor) dashboardPlugins() []Plugin {
	var dps []Plugin
	for _, p := range c.exts {
		if dp, ok := p.(Plugin); ok {
			dps = append(dps, dp)
		}
	}
	return dps
}

func (c *Contributor) collectPluginSections(ctx context.Context) []templ.Component {
	var sections []templ.Component
	for _, dp := range c.dashboardPlugins() {
		for _, w := range dp.DashboardWidgets(ctx) {
			sections = append(sections, w.Render(ctx))
		}
	}
	return sections
}

func (c *Contributor) collectPluginSettings(ctx context.Context) []templ.Component {
	var panels []templ.Component
	for _, dp := range c.dashboardPlugins() {
		if panel := dp.DashboardSettingsPanel(ctx); panel != nil {
			panels = append(panels, panel)
		}
	}
	return panels
}

func (c *Contributor) collectCollectionDetailSections(ctx context.Context, colID id.CollectionID) []templ.Component {
	var sections []templ.Component
	for _, p := range c.exts {
		if cdc, ok := p.(CollectionDetailContributor); ok {
			if section := cdc.DashboardCollectionDetailSection(ctx, colID); section != nil {
				sections = append(sections, section)
			}
		}
	}
	return sections
}

func (c *Contributor) collectDocumentDetailSections(ctx context.Context, docID id.DocumentID) []templ.Component {
	var sections []templ.Component
	for _, p := range c.exts {
		if ddc, ok := p.(DocumentDetailContributor); ok {
			if section := ddc.DashboardDocumentDetailSection(ctx, docID); section != nil {
				sections = append(sections, section)
			}
		}
	}
	return sections
}
