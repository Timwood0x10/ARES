// Package tenantctx carries the request-scoped tenant through the execution
// path. It is the single carrier between the submission boundary (which knows
// the caller's tenant), the fabric checkpoint envelope (which persists it
// across the scheduler's asynchronous execution), and the knowledge read path
// (which scopes recall to the same corpus the write path distilled into).
//
// It is deliberately a leaf package: every layer — kernel scheduler, fabric
// cognitions, knowledge providers — may import it without creating cycles.
package tenantctx

import "context"

// tenantKey is the context key type. A private type prevents collisions with
// keys declared by other packages.
type tenantKey struct{}

// With returns a context carrying tenant. An empty tenant is stored as-is:
// FromContext then reports "", which every consumer treats as "no tenant
// known" and falls back to its own default — an explicit empty is never
// distinguished from an absent value, so callers cannot accidentally "reset"
// a tenant that a closer scope set.
func With(ctx context.Context, tenant string) context.Context {
	return context.WithValue(ctx, tenantKey{}, tenant)
}

// From returns the tenant carried by ctx, or "" when none is set.
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(tenantKey{}).(string)
	return v
}
