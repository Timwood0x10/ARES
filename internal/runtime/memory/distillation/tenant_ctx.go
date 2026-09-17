package distillation

import "context"

// tenantCtxKey is the context key under which the write-side tenant travels.
// It is unexported so only this package can set or read it.
type tenantCtxKey struct{}

// WithTenant returns a copy of ctx carrying the tenant that distillation
// writes must land under.
//
// The ExperienceRepository write methods (Create/Update/Delete/DeleteBatch)
// take no tenant argument because the llmexp Experience DTO has no TenantID
// field. Without this seam an experienceadapters.DistillationRepo is pinned
// to the tenant it was constructed with, so a runtime SetDefaultTenantID
// override re-scoped reads while writes kept landing in the construction-time
// tenant. Callers that know the tenant inject it here; the adapter reads it
// back and falls back to its own default when absent.
func WithTenant(ctx context.Context, tenantID string) context.Context {
	if tenantID == "" {
		return ctx
	}
	return context.WithValue(ctx, tenantCtxKey{}, tenantID)
}

// TenantFrom returns the tenant carried by ctx, or "" when none was set.
// It is the read side of WithTenant, used by repository adapters that must
// stamp a tenant onto rows whose DTO cannot carry one.
func TenantFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	tenantID, _ := ctx.Value(tenantCtxKey{}).(string)
	return tenantID
}
