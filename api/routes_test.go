package api_test

import (
	"strings"
	"testing"

	"github.com/xraph/forge"

	"github.com/xraph/weave/api"
)

// refusingRouter is a real forge router that refuses every route: a group
// built with a nil middleware fails each registration under it.
func refusingRouter() forge.Router {
	return forge.NewRouter().Group("/weave", forge.WithGroupMiddleware(nil))
}

func TestRegisterRoutesReportsEveryRouteThatFailsToRegister(t *testing.T) {
	err := api.New(newEngine(t), nil).RegisterRoutes(refusingRouter())
	if err == nil {
		t.Fatal("RegisterRoutes on a router that refuses every route returned nil")
	}
	for _, op := range []string{
		"createCollection", "listCollections", "getCollection", "deleteCollection", "collectionStats", "reindexCollection",
		"ingestDocument", "ingestBatch", "listDocuments", "deleteDocumentsBySource", "getDocument", "deleteDocument",
		"retrieve", "hybridSearch",
	} {
		if !strings.Contains(err.Error(), "weave: register route "+op+": ") {
			t.Errorf("the error does not name %s: %v", op, err)
		}
	}
}

func TestRegisterRoutesSucceedsOnAWorkingRouter(t *testing.T) {
	router := forge.NewRouter()
	if err := api.New(newEngine(t), nil).RegisterRoutes(router.Group("/weave")); err != nil {
		t.Fatalf("RegisterRoutes: %v", err)
	}
	if n := len(router.Routes()); n != 14 {
		t.Errorf("registered %d routes, want 14", n)
	}
}

func TestHandlerPanicsWhenARouteFailsToRegister(t *testing.T) {
	defer func() {
		r := recover()
		err, ok := r.(error)
		// forge's own reason travels with it.
		if !ok || !strings.Contains(err.Error(), "weave: register route") || !strings.Contains(err.Error(), "is nil") {
			t.Fatalf("recovered %v, want the registration error with forge's reason", r)
		}
	}()
	api.New(newEngine(t), refusingRouter()).Handler()
	t.Fatal("Handler returned on a router that refuses every route")
}
