package extension

import (
	"strings"
	"testing"

	"github.com/xraph/forge"

	"github.com/xraph/weave/store/memory"
)

// A route the app's router refuses fails Register, so the app never boots
// with part of Weave's API missing.
func TestRegisterFailsWhenARouteCannotRegister(t *testing.T) {
	app := forge.New(forge.WithAppName("t"))
	// forge refuses to register a route behind a nil middleware.
	app.Router().Use(nil)

	err := New(WithStore(memory.New())).Register(app)
	if err == nil || !strings.Contains(err.Error(), "weave: register route createCollection: ") {
		t.Fatalf("Register: %v, want the refused routes named", err)
	}
}

func TestRegisterRoutesReturnsTheRegistrationError(t *testing.T) {
	ext := New(WithStore(memory.New()), WithDisableRoutes())
	if err := ext.Register(forge.New(forge.WithAppName("t"))); err != nil {
		t.Fatalf("Register: %v", err)
	}
	broken := forge.NewRouter().Group("/weave", forge.WithGroupMiddleware(nil))
	if err := ext.RegisterRoutes(broken); err == nil || !strings.Contains(err.Error(), "weave: register route hybridSearch: ") {
		t.Fatalf("RegisterRoutes: %v, want the refused routes named", err)
	}
	if err := ext.RegisterRoutes(forge.NewRouter()); err != nil {
		t.Fatalf("RegisterRoutes on a working router: %v", err)
	}
}
