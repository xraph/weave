package dashboard

import (
	"context"

	"github.com/xraph/forge/extensions/dashboard/contributor"

	"github.com/xraph/weave/plugins"
)

// NewManifest builds a contributor.Manifest for the weave dashboard.
func NewManifest(exts []plugins.Extension) *contributor.Manifest {
	m := &contributor.Manifest{
		Name:        "weave",
		DisplayName: "Weave",
		Icon:        "layers",
		Version:     "0.1.0",
		Layout:      "extension",
		ShowSidebar: boolPtr(true),
		TopbarConfig: &contributor.TopbarConfig{
			Title:       "Weave",
			LogoIcon:    "layers",
			AccentColor: "#10b981",
			ShowSearch:  true,
			Actions: []contributor.TopbarAction{
				{Label: "API Docs", Icon: "file-text", Href: "/docs", Variant: "ghost"},
			},
		},
		Nav:      baseNav(),
		Widgets:  baseWidgets(),
		Settings: baseSettings(),
		Capabilities: []string{
			"searchable",
		},
	}

	// Merge plugin-contributed nav items and widgets.
	for _, p := range exts {
		if dpc, ok := p.(PageContributor); ok {
			m.Nav = append(m.Nav, dpc.DashboardNavItems()...)
		}

		dp, ok := p.(Plugin)
		if !ok {
			continue
		}

		for _, pp := range dp.DashboardPages() {
			m.Nav = append(m.Nav, contributor.NavItem{
				Label:    pp.Label,
				Path:     pp.Route,
				Icon:     pp.Icon,
				Group:    "Weave",
				Priority: 10,
			})
		}

		for _, pw := range dp.DashboardWidgets(context.Background()) {
			m.Widgets = append(m.Widgets, contributor.WidgetDescriptor{
				ID:         pw.ID,
				Title:      pw.Title,
				Size:       pw.Size,
				RefreshSec: pw.RefreshSec,
				Group:      "Weave",
			})
		}
	}

	return m
}

func baseNav() []contributor.NavItem {
	return []contributor.NavItem{
		{Label: "Overview", Path: "/", Icon: "layout-dashboard", Group: "Weave", Priority: 0},
		{Label: "Retrieval", Path: "/retrieval", Icon: "search", Group: "Weave", Priority: 1},
		{Label: "Pipeline", Path: "/pipeline", Icon: "workflow", Group: "Weave", Priority: 2},
		{Label: "Collections", Path: "/collections", Icon: "folders", Group: "Content", Priority: 3},
		{Label: "Documents", Path: "/documents", Icon: "file-text", Group: "Content", Priority: 4},
		{Label: "Chunks", Path: "/chunks", Icon: "puzzle", Group: "Content", Priority: 5},
		{Label: "Loaders", Path: "/loaders", Icon: "upload", Group: "Reference", Priority: 6},
		{Label: "Extensions", Path: "/extensions", Icon: "plug", Group: "Reference", Priority: 7},
	}
}

func baseWidgets() []contributor.WidgetDescriptor {
	return []contributor.WidgetDescriptor{
		{
			ID:          "weave-stats",
			Title:       "RAG Stats",
			Description: "Collection, document, and chunk counts",
			Size:        "md",
			RefreshSec:  60,
			Group:       "Weave",
		},
		{
			ID:          "weave-recent-ingestions",
			Title:       "Recent Ingestions",
			Description: "Recently ingested documents",
			Size:        "lg",
			RefreshSec:  15,
			Group:       "Weave",
		},
		{
			ID:          "weave-pipeline-health",
			Title:       "Pipeline Health",
			Description: "RAG pipeline component status",
			Size:        "md",
			RefreshSec:  60,
			Group:       "Weave",
		},
	}
}

func baseSettings() []contributor.SettingsDescriptor {
	return []contributor.SettingsDescriptor{
		{
			ID:          "weave-config",
			Title:       "Engine Settings",
			Description: "Configure Weave RAG engine behavior",
			Group:       "Weave",
			Icon:        "layers",
		},
	}
}

func boolPtr(b bool) *bool { return &b }
