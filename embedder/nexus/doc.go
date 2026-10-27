// Package nexus is a weave embedder that sends every request through a
// nexus gateway, so embedding spend lands on the same usage records, keys
// and budgets as the rest of your model traffic.
//
// It lives in its own module, github.com/xraph/weave/embedder/nexus, so the
// root weave module never pulls in nexus. Build it with:
//
//	emb := nexus.New(ext.Gateway, "text-embedding-3-small", 1536,
//		nexus.WithProvider("knowledge"))
//
// Every call needs a weave tenant on the context (weave.WithTenant). The
// spend goes to the nexus tenant on the context when the host set one with
// nexus's pipeline.WithTenantID, and otherwise to the weave tenant passed
// through WithTenantMapper (by default, unchanged).
//
// Every request asks for exactly the configured dimensions, so the provider
// nexus routes to has to be able to shorten vectors for the model. On
// OpenAI's own endpoint that means text-embedding-3-small or
// text-embedding-3-large.
package nexus
