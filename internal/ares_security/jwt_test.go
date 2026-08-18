package ares_security

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	token, err := SignJWT([]byte("s3cret"), "alice", "operator", time.Hour, now)
	if err != nil {
		t.Fatalf("SignJWT: %v", err)
	}
	if !strings.Contains(token, ".") || len(strings.Split(token, ".")) != 3 {
		t.Fatalf("token must be three base64url segments, got %q", token)
	}
	sub, role, err := VerifyJWT([]byte("s3cret"), token, now.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("VerifyJWT: %v", err)
	}
	if sub != "alice" || role != "operator" {
		t.Fatalf("VerifyJWT = (%q, %q), want (alice, operator)", sub, role)
	}
}

func TestVerifyJWTRejectsWrongSecret(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	token, _ := SignJWT([]byte("s3cret"), "alice", "admin", time.Hour, now)
	if _, _, err := VerifyJWT([]byte("other"), token, now); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken for wrong secret, got %v", err)
	}
}

func TestVerifyJWTRejectsExpired(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	token, _ := SignJWT([]byte("s3cret"), "alice", "admin", time.Minute, now)
	if _, _, err := VerifyJWT([]byte("s3cret"), token, now.Add(2*time.Minute)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}

func TestVerifyJWTRejectsTamperedPayload(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	token, _ := SignJWT([]byte("s3cret"), "alice", "agent", time.Hour, now)
	// Flip a char in the payload segment (index 1) — signature must fail.
	parts := strings.Split(token, ".")
	payload := []byte(parts[1])
	payload[len(payload)/2] ^= 0x01
	parts[1] = string(payload)
	tampered := strings.Join(parts, ".")
	if _, _, err := VerifyJWT([]byte("s3cret"), tampered, now); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("want ErrInvalidToken for tampered payload, got %v", err)
	}
}

func TestVerifyJWTRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "abc", "a.b", "a.b.c.d", "not a token at all"} {
		if _, _, err := VerifyJWT([]byte("s3cret"), bad, time.Now()); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("VerifyJWT(%q) = %v, want ErrInvalidToken", bad, err)
		}
	}
}

func TestSignJWTValidation(t *testing.T) {
	now := time.Now()
	if _, err := SignJWT(nil, "a", "admin", time.Hour, now); err == nil {
		t.Fatal("empty secret must error")
	}
	if _, err := SignJWT([]byte("s"), "", "admin", time.Hour, now); err == nil {
		t.Fatal("empty subject must error")
	}
	if _, err := SignJWT([]byte("s"), "a", "", time.Hour, now); err == nil {
		t.Fatal("empty role must error")
	}
	if _, err := SignJWT([]byte("s"), "a", "admin", 0, now); err == nil {
		t.Fatal("zero ttl must error")
	}
}

func TestVerifyJWTRejectsFutureIssued(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	// Sign at 12:00, verify at 11:00 — token is not yet valid.
	token, _ := SignJWT([]byte("s3cret"), "alice", "admin", time.Hour, now)
	if _, _, err := VerifyJWT([]byte("s3cret"), token, now.Add(-time.Hour)); !errors.Is(err, ErrTokenTooEarly) {
		t.Fatalf("want ErrTokenTooEarly, got %v", err)
	}
}
