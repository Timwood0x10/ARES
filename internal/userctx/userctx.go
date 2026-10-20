// Package userctx carries the request-scoped user through the execution path.
// It is the single carrier between the submission boundary (which knows the
// caller's user), the fabric checkpoint payload (which persists it across the
// scheduler's asynchronous execution), and the knowledge read path (which
// scopes recall to the same user graph the write path distilled into).
//
// It is deliberately a leaf package, mirroring tenantctx: every layer — kernel
// scheduler, fabric cognitions, knowledge providers — may import it without
// creating cycles.
//
// The user is distinct from the tenant: the tenant is the outer isolation axis
// and the user is the inner one, so a namespace is normally tenant+user. An
// empty user means "no user known" and consumers fall back to the tenant
// scope, preserving pre-M5 behaviour.
package userctx

import "context"

// userKey is the context key type. A private type prevents collisions with
// keys declared by other packages.
type userKey struct{}

// With returns a context carrying user. An empty user is stored as-is: From
// then reports "", which every consumer treats as "no user known" and falls
// back to its own default — an explicit empty is never distinguished from an
// absent value, so callers cannot accidentally "reset" a user that a closer
// scope set.
func With(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// From returns the user carried by ctx, or "" when none is set.
func From(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(userKey{}).(string)
	return v
}
