package knowledge

import "context"

// userCtxKey is the context key for the request-scoped user ID (M5).
// It is unexported so only this package can set it, preventing collisions
// with keys declared by other packages.
type userCtxKey struct{}

// WithUserContext returns a context carrying userID, so the read-side
// StoreProvider can resolve the user-level namespace for recall (M5).
// An empty userID is stored as-is: ResolveUserNamespace treats it as
// "no user" and falls back to the tenant namespace.
//
// TODO(tech-debt): nothing in production calls this yet, so the read side
// never resolves a user namespace (UserFromContext always returns ""). The
// mirror point is the scheduler, which already stamps the tenant onto the
// quantum context — see tenantctx.With in
// internal/kernel/scheduler_quantum.go. That needs a user dimension on the
// task envelope first (models.Task has none today). Until then M5's read path
// is inert; see also the write-side TODO in
// internal/ares_bootstrap/knowledge_akg.go.
func WithUserContext(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userCtxKey{}, userID)
}

// UserFromContext returns the user ID carried by ctx, or "" when none
// is set. The read side uses this to resolve the same user-level namespace
// the write side (DistillBridge) distilled facts into.
func UserFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(userCtxKey{}).(string)
	return v
}
