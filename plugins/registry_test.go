package plugins

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

type failingHook struct{ name string }

func (f failingHook) Name() string { return f.name }

func (failingHook) OnDocumentDeleted(context.Context, id.DocumentID) error {
	return errors.New("could not audit document about alice@example.com")
}

func TestHookErrorsLogTheTypeAndAreThrottled(t *testing.T) {
	logger := log.NewTestLogger()
	tl, ok := logger.(*log.TestLogger)
	if !ok {
		t.Fatalf("NewTestLogger returned %T", logger)
	}
	r := NewRegistry(logger)
	clock := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return clock }
	r.Register(failingHook{"audit"})
	r.Register(failingHook{"mirror"})

	// Twenty events fail at once, as they do when a hook's backend is down
	// under load: one line per plugin, the rest counted.
	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { r.EmitDocumentDeleted(context.Background(), id.NewDocumentID()) })
	}
	wg.Wait()
	logs := tl.GetLogsByLevel("WARN")
	if len(logs) != 2 {
		t.Fatalf("got %d warn lines, want one per plugin", len(logs))
	}

	// A minute on, the next failure logs and says how many it stood for.
	clock = clock.Add(time.Minute)
	r.EmitDocumentDeleted(context.Background(), id.NewDocumentID())
	logs = tl.GetLogsByLevel("WARN")
	if len(logs) != 4 {
		t.Fatalf("got %d warn lines after a minute, want 4", len(logs))
	}
	for _, e := range logs[2:] {
		if v, _ := e.Field("suppressed"); fmt.Sprint(v) != fmt.Sprint(n-1) {
			t.Errorf("suppressed = %v, want %d", v, n-1)
		}
	}

	for _, e := range logs {
		if v, _ := e.Field("error_type"); v != "*errors.errorString" {
			t.Errorf("error_type = %v", v)
		}
		if _, ok := e.Field("error"); ok {
			t.Error("the error's text was logged")
		}
		for _, f := range e.Fields {
			if s, ok := f.Value().(string); ok && strings.Contains(s, "alice@example.com") {
				t.Errorf("field %s carries the hook's error text", f.Key())
			}
		}
	}
}
