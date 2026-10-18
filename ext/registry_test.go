package ext

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/xraph/go-utils/log"

	"github.com/xraph/weave/id"
)

type failingHook struct{}

func (failingHook) Name() string { return "audit" }

func (failingHook) OnDocumentDeleted(context.Context, id.DocumentID) error {
	return errors.New("could not audit document about alice@example.com")
}

// ext.Registry isn't the engine's registry, but it's exported, so its hook
// error line gets the same treatment as plugins.Registry's.
func TestHookErrorsLogTheTypeAndAreThrottled(t *testing.T) {
	logger := log.NewTestLogger()
	tl, ok := logger.(*log.TestLogger)
	if !ok {
		t.Fatalf("NewTestLogger returned %T", logger)
	}
	r := NewRegistry(logger)
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	r.Register(failingHook{})

	const n = 10
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { r.EmitDocumentDeleted(context.Background(), id.NewDocumentID()) })
	}
	wg.Wait()
	clock = clock.Add(time.Minute)
	r.EmitDocumentDeleted(context.Background(), id.NewDocumentID())

	logs := tl.GetLogsByLevel("WARN")
	if len(logs) != 2 {
		t.Fatalf("got %d warn lines, want 2", len(logs))
	}
	if v, _ := logs[1].Field("suppressed"); fmt.Sprint(v) != fmt.Sprint(n-1) {
		t.Errorf("suppressed = %v, want %d", v, n-1)
	}
	for _, e := range logs {
		if v, _ := e.Field("error_type"); v != "*errors.errorString" {
			t.Errorf("error_type = %v", v)
		}
		for _, f := range e.Fields {
			if s, ok := f.Value().(string); ok && strings.Contains(s, "alice@example.com") {
				t.Errorf("field %s carries the hook's error text", f.Key())
			}
		}
	}
}
