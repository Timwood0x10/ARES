package knowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// UserNamespace encodes a tenant+user pair into a single namespace string
// for the "one user one graph" isolation model (M5).
//
// The encoding MUST be collision-safe: a tenant or user ID containing the
// separator character must not produce a key that collides with a different
// pair. The naive approach of joining with "/" fails when tenant="a/b",
// user="c" collides with tenant="a", user="b/c". Instead, each component is
// hashed to a fixed-width hex digest, and the two digests are joined with a
// separator that cannot appear inside a hex digest.
//
// When tenantID is empty the namespace is the user ID alone (still hashed
// for consistency): a user with no tenant gets their own graph, not the
// shared "default" graph.
//
// When userID is also empty, the function returns the empty string so the
// caller can fall back to the existing tenant namespace or the "default"
// namespace — preserving pre-0.3.4 behaviour.
func UserNamespace(tenantID, userID string) string {
	if userID == "" {
		// No user: no user-level namespace. Caller falls back to tenant
		// namespace or "default".
		return ""
	}
	if tenantID == "" {
		// User-only namespace (no tenant): hash the user ID for a
		// consistent, collision-safe key.
		return "u:" + shortHash(userID)
	}
	// Combined: hash both and join with ":". The prefix "tu:" distinguishes
	// combined keys from user-only keys so a user "u:abc" can never collide
	// with a combined key.
	return "tu:" + shortHash(tenantID) + ":" + shortHash(userID)
}

// shortHash returns the first 16 hex characters of the SHA-256 hash of s.
// 16 hex chars = 64 bits, which gives a collision space of ~10^19 —
// sufficient for namespace keys (birthday bound: ~10^9 keys before a 1%
// collision chance, far beyond any single deployment's user count).
func shortHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// ResolveUserNamespace resolves the effective namespace for a knowledge
// operation given a tenant, user, and the store's fallback namespace. It
// implements the precedence:
//
//  1. UserNamespace(tenant, user) when user is present (M5: one user one graph).
//  2. tenantID when user is absent but tenant is present (pre-M5 behaviour).
//  3. fallback (the store's static namespace, e.g. "default").
//
// This keeps the write side (DistillBridge) and the read side (StoreProvider)
// in lockstep: both call this function to resolve the namespace for a given
// tenant+user pair.
func ResolveUserNamespace(tenantID, userID, fallback string) string {
	if ns := UserNamespace(tenantID, userID); ns != "" {
		return ns
	}
	if tenantID != "" {
		return tenantID
	}
	return fallback
}

// NamespaceKey represents a parsed tenant+user pair from a namespace string
// produced by UserNamespace. It is used for testing and debugging.
type NamespaceKey struct {
	TenantID string
	UserID   string
}

// String returns a human-readable representation for logging.
func (k NamespaceKey) String() string {
	if k.TenantID == "" && k.UserID == "" {
		return "(empty)"
	}
	return fmt.Sprintf("tenant=%q user=%q", k.TenantID, k.UserID)
}
