package telemetry

import (
	"sync"
	"time"
)

// HookErrorInterval is how often one hook of one extension may log an
// error. Errors in between are counted and reported on the next line, so an
// extension that fails on every event during an outage logs once a minute,
// not once per document.
const HookErrorInterval = time.Minute

// HookErrors throttles hook error log lines per hook and extension. The zero
// value is ready to use and safe for concurrent use.
type HookErrors struct {
	mu    sync.Mutex
	state map[hookErrKey]*hookErrState
}

type hookErrKey struct{ hook, ext string }

type hookErrState struct {
	last       time.Time
	suppressed int
}

// Allow reports whether an error from hook in ext may be logged at now, and
// how many errors from the same pair it stood for since the last line.
func (h *HookErrors) Allow(hook, ext string, now time.Time) (ok bool, suppressed int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == nil {
		h.state = make(map[hookErrKey]*hookErrState)
	}
	key := hookErrKey{hook, ext}
	st, found := h.state[key]
	if !found {
		st = &hookErrState{}
		h.state[key] = st
	}
	if !st.last.IsZero() && now.Sub(st.last) < HookErrorInterval {
		st.suppressed++
		return false, 0
	}
	suppressed = st.suppressed
	st.last, st.suppressed = now, 0
	return true, suppressed
}
