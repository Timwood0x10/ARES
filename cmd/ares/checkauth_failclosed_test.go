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
