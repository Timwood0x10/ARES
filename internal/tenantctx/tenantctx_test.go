package tenantctx

import (
	"context"
	"testing"
)

// TestWithFromRoundTrip pins the carrier: a tenant set on a context is read
// back verbatim, nesting overwrites (innermost scope wins), and an absent or
// empty tenant reads as "" — the value every consumer treats as "no tenant
// known" and falls back to its own default.
func TestWithFromRoundTrip(t *testing.T) {
	if got := From(context.Background()); got != "" {
		t.Fatalf("background context must read empty tenant, got %q", got)
	}
	//lint:ignore SA1012 intentional: From explicitly defends against a nil ctx (see the ctx==nil branch), and that defense is what the next line pins.
	if got := From(nil); got != "" { //nolint:staticcheck
		t.Fatalf("nil context must read empty tenant, got %q", got)
	}

	ctx := With(context.Background(), "tenant-acme")
	if got := From(ctx); got != "tenant-acme" {
		t.Fatalf("round trip: got %q, want tenant-acme", got)
	}

	inner := With(ctx, "tenant-other")
	if got := From(inner); got != "tenant-other" {
		t.Fatalf("innermost scope must win: got %q, want tenant-other", got)
	}

	empty := With(ctx, "")
	if got := From(empty); got != "" {
		t.Fatalf("explicit empty must read as empty (no accidental reset semantics), got %q", got)
	}
}
