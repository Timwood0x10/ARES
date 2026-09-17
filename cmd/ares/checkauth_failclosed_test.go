package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Timwood0x10/ares/internal/introspect"
)

// TestCheckAuthRead_FailClosedWithoutCredentials is the #P0-9 regression:
// with NO credential layer configured, checkAuthRead used to allow every
// request unconditionally — `--host 0.0.0.0` with no credentials exposed the
// raw introspect event stream to the network. The no-credentials posture must
// be loopback-only: a non-loopback client gets 401, a loopback client keeps
// the documented local development path.
func TestCheckAuthRead_FailClosedWithoutCredentials(t *testing.T) {
	h := &actionHandler{
		inner: http.NotFoundHandler(),
		intro: introspect.NewHandler(&introspect.Store{}),
	}

	t.Run("non-loopback denied", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil)
		// httptest defaults RemoteAddr to 192.0.2.1:1234 (non-loopback).
		ok := h.checkAuthRead(rec, req)
		if ok {
			t.Fatal("checkAuthRead allowed a non-loopback request with no credentials configured")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("loopback allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil)
		req.RemoteAddr = "127.0.0.1:55555"
		if !h.checkAuthRead(rec, req) {
			t.Fatalf("checkAuthRead denied a loopback request with no credentials configured (status %d)", rec.Code)
		}
	})
}

// TestCheckAuthRead_ProxiedLoopbackIsNotTrusted is the E-1 regression: the
// loopback trust model must not survive a same-host reverse proxy.
//
// A proxy on the same machine terminates the TCP connection itself, so every
// forwarded request arrives with RemoteAddr == 127.0.0.1. Treating that as
// "local operator" handed the read API (task inputs, checkpoints, config) to
// any network client whenever the operator put nginx/caddy in front of serve
// with no credential configured.
//
// A request that carries proxy forwarding headers did not originate on this
// host, so it must be challenged like any other remote client.
func TestCheckAuthRead_ProxiedLoopbackIsNotTrusted(t *testing.T) {
	h := &actionHandler{
		inner: http.NotFoundHandler(),
		intro: introspect.NewHandler(&introspect.Store{}),
	}

	headers := []string{
		"X-Forwarded-For",
		"Forwarded",
		"X-Real-IP",
		"X-Forwarded-Host",
		"X-Forwarded-Proto",
		"X-Client-IP",
	}
	for _, name := range headers {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil)
			// Loopback peer: the proxy. The header says the real client is
			// elsewhere.
			req.RemoteAddr = "127.0.0.1:55555"
			req.Header.Set(name, "203.0.113.7")

			if h.checkAuthRead(rec, req) {
				t.Fatalf("checkAuthRead trusted a proxied loopback request carrying %s; the proxy bypass is open", name)
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}
}

// TestAuthorize_AuthLocalRejectsProxiedLoopback locks the same rule on the
// authLocal route level (the "localhost only" endpoints): a proxied request
// must not be treated as local either.
func TestAuthorize_AuthLocalRejectsProxiedLoopback(t *testing.T) {
	h := &actionHandler{inner: http.NotFoundHandler()}

	t.Run("direct loopback allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/chaos/live", nil)
		req.RemoteAddr = "127.0.0.1:55555"
		if _, ok := h.authorize(authLocal, rec, req); !ok {
			t.Fatalf("direct loopback must pass authLocal (status %d)", rec.Code)
		}
	})

	t.Run("proxied loopback denied", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/chaos/live", nil)
		req.RemoteAddr = "127.0.0.1:55555"
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		if _, ok := h.authorize(authLocal, rec, req); ok {
			t.Fatal("a proxied request must not pass authLocal")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
}
