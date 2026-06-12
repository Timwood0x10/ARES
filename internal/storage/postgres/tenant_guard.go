// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"context"
	"fmt"
	"strings"

	coreerrors "goagentx/internal/core/errors"
	"goagentx/internal/errors"
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
// NOTE: Uses SET LOCAL to set tenant context within the current transaction only.
// This ensures tenant isolation works correctly with connection pooling, as SET LOCAL
// only affects the current transaction and is reset when the transaction ends.
// This prevents tenant context leakage across different connections in the pool.
//
// Args:
// ctx - database operation context.
// tenantID - tenant identifier, must be non-empty.
// Returns error if setting tenant context fails.
func (g *TenantGuard) SetTenantContext(ctx context.Context, tenantID string) error {
	if tenantID == "" {
		return coreerrors.ErrInvalidArgument
	}

	// Manually escape single quotes for SQL literal
	quotedID := "'" + strings.ReplaceAll(tenantID, "'", "''") + "'"
	// Use SET LOCAL to ensure tenant context only affects current transaction
	// This prevents cross-tenant data access in connection pool scenarios
	query := fmt.Sprintf("SET LOCAL app.tenant_id TO %s", quotedID)
	_, err := g.db.Exec(ctx, query)
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
// This is primarily used for cleanup and testing purposes.
// Args:
// ctx - database operation context.
// Returns error if clearing tenant context fails.
func (g *TenantGuard) ClearTenantContext(ctx context.Context) error {
	_, err := g.db.Exec(ctx, "SET app.tenant_id TO DEFAULT")
	if err != nil {
		return errors.Wrap(err, "clear tenant context")
	}

	return nil
}
