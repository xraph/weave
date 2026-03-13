package components

import (
	"github.com/a-h/templ"
	"github.com/xraph/forgeui/icons"
)

func resolveIcon(iconName string, opts ...icons.Option) templ.Component {
	switch iconName {
	case "layers":
		return icons.Layers(opts...)
	case "layout-dashboard":
		return icons.LayoutDashboard(opts...)
	case "folders":
		return icons.Folders(opts...)
	case "file-text":
		return icons.FileText(opts...)
	case "puzzle":
		return icons.Puzzle(opts...)
	case "search":
		return icons.Search(opts...)
	case "workflow":
		return icons.Workflow(opts...)
	case "upload":
		return icons.Upload(opts...)
	case "plug":
		return icons.Plug(opts...)
	case "database":
		return icons.Database(opts...)
	case "alert-circle":
		return icons.CircleAlert(opts...)
	case "check-circle":
		return icons.CircleCheck(opts...)
	case "clock":
		return icons.Clock(opts...)
	case "hash":
		return icons.Hash(opts...)
	case "activity":
		return icons.Activity(opts...)
	case "zap":
		return icons.Zap(opts...)
	case "settings":
		return icons.Settings(opts...)
	default:
		return icons.Info(opts...)
	}
}
