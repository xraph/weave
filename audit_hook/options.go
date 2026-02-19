package audithook

import "log/slog"

// Option configures an audit hook Extension.
type Option func(*Extension)

// WithActions limits the extension to recording only the listed actions.
// By default all actions are recorded.
func WithActions(actions ...string) Option {
	return func(e *Extension) {
		e.enabled = make(map[string]bool, len(actions))
		for _, a := range actions {
			e.enabled[a] = true
		}
	}
}

// WithLogger sets the logger used for internal warnings.
func WithLogger(l *slog.Logger) Option {
	return func(e *Extension) {
		e.logger = l
	}
}
