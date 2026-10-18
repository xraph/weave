package plugins

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

	// Five failures inside a minute: one line per plugin.
	for range 5 {
		r.EmitDocumentDeleted(context.Background(), id.NewDocumentID())
		clock = clock.Add(5 * time.Second)
	}
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
	if v, _ := logs[2].Field("suppressed"); fmt.Sprint(v) != "4" {
		t.Errorf("suppressed = %v, want 4", v)
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
