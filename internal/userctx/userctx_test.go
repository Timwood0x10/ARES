package userctx_test

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// TestUserContext_AbsentPinsEmpty pins that a context carrying no user reports
// "" — the value every consumer reads as "no user known".
func TestUserContext_AbsentPinsEmpty(t *testing.T) {
	t.Parallel()

	if got := userctx.From(context.Background()); got != "" {
		t.Errorf("From(empty context) = %q, want empty", got)
	}
	// The guard against a nil context is deliberate, so it is pinned here. A
	// typed nil variable (not a bare nil literal) keeps staticcheck's SA1012 —
	// which forbids passing an untyped nil Context — from firing on it.
	var nilCtx context.Context
	if got := userctx.From(nilCtx); got != "" {
		t.Errorf("From(nil) = %q, want empty", got)
	}
}

// TestUserContext_RoundTrip pins that With and From carry the user through the
// context, and that the user and tenant scopes are independent carriers — the
// scheduler stamps both onto the same context, so neither may disturb the
// other (M5).
func TestUserContext_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx := userctx.With(context.Background(), "user-1")
	if got := userctx.From(ctx); got != "user-1" {
		t.Errorf("From = %q, want %q", got, "user-1")
	}

	ctx = tenantctx.With(ctx, "tenant-1")
	if got := userctx.From(ctx); got != "user-1" {
		t.Errorf("From after tenantctx.With = %q, want %q", got, "user-1")
	}
	if got := tenantctx.From(ctx); got != "tenant-1" {
		t.Errorf("tenantctx.From = %q, want %q", got, "tenant-1")
	}
}

// TestUserContext_EmptyStoredAsEmpty pins that an explicit empty is stored
// as-is and reads back as "" — never distinguished from an absent value, so a
// consumer resolves "no user" in both cases.
func TestUserContext_EmptyStoredAsEmpty(t *testing.T) {
	t.Parallel()

	ctx := userctx.With(context.Background(), "")
	if got := userctx.From(ctx); got != "" {
		t.Errorf("From(With(\"\")) = %q, want empty", got)
	}
}
