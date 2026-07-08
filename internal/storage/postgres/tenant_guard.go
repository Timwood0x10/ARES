// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"context"

	"github.com/Timwood0x10/ares/internal/errors"
)

// TenantGuard provides physical isolation for multi-tenant data access.
// This enforces tenant context at the database level to prevent cross-tenant data access.
type TenantGuard struct {
	db *Pool
}

// NewTenantGuard creates a new TenantGuard instance.
func NewTenantGuard(pool *Pool) *TenantGuard {
	return &TenantGuard{db: pool}
}

// SetTenantContext sets the tenant context for the current database session.
// This MUST be called for every tenant-specific operation to ensure physical isolation.
//
// Uses set_config('app.tenant_id', $1, true) with the third argument `true` to
// apply SET LOCAL semantics: the setting is scoped to the current transaction
// and reverts when the transaction ends. This prevents tenant context leakage
// across pooled connections. The value is passed as a parameterized argument,
// avoiding manual string escaping.
//
// Args:
// ctx - database operation context.
// tenantID - tenant identifier, must be non-empty.
// Returns error if setting tenant context fails.
func (g *TenantGuard) SetTenantContext(ctx context.Context, tenantID string) error {
	if err := validateUserInput(tenantID, 255); err != nil {
		return err
	}

	// set_config(name, value, is_local) with is_local=true is equivalent to
	// SET LOCAL but accepts the value as a parameter, eliminating the need
	// for manual single-quote escaping.
	_, err := g.db.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID)
	if err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	return nil
}

// MustSetTenantContext sets the tenant context and returns error on failure.
// This should only be used in initialization paths where failure is fatal.
// Args:
// ctx - database operation context.
// tenantID - tenant identifier.
// Returns:
// error - if tenant context setup fails.
func (g *TenantGuard) MustSetTenantContext(ctx context.Context, tenantID string) error {
	if err := g.SetTenantContext(ctx, tenantID); err != nil {
		return errors.Wrap(err, "failed to set tenant context")
	}
	return nil
}

// WithTenant executes a function within a tenant context.
// This is a convenience wrapper that ensures tenant context is set before execution.
// Args:
// ctx - database operation context.
// tenantID - tenant identifier.
// fn - function to execute within tenant context.
// Returns error if tenant context setup or function execution fails.
func (g *TenantGuard) WithTenant(ctx context.Context, tenantID string, fn func(context.Context) error) error {
	if err := g.SetTenantContext(ctx, tenantID); err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	return fn(ctx)
}

// ClearTenantContext clears the tenant context.
// Uses SET LOCAL (via set_config with is_local=true) so the reset only applies
// to the current transaction. The previous SET (session-level) variant would
// persist on pooled connections and could break RLS for subsequent requests
// that reuse the same connection without setting their own tenant context.
//
// Args:
// ctx - database operation context.
// Returns error if clearing tenant context fails.
func (g *TenantGuard) ClearTenantContext(ctx context.Context) error {
	_, err := g.db.Exec(ctx, "SELECT set_config('app.tenant_id', '', true)")
	if err != nil {
		return errors.Wrap(err, "clear tenant context")
	}

	return nil
}
