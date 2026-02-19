package weave

import "context"

// Context is the execution context for Weave operations.
// It is a simple alias for context.Context. Scope is injected via
// forge.WithScope on the stdlib context.
type Context = context.Context
