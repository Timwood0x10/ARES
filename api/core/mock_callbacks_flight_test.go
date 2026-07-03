package core

import (
	"context"
	"net/http"
)

// Ensure compile-time checks.
var _ CallbackHandler = (*mockCallbackHandler)(nil)
var _ CallbackRegistry = (*mockCallbackRegistry)(nil)
var _ FlightRecorder = (*mockFlightRecorder)(nil)
var _ ContextCleaner = (*mockContextCleaner)(nil)
var _ MCPManager = (*mockMCPManager)(nil)
var _ Dashboard = (*mockDashboard)(nil)

// ── mockCallbackHandler ────────────────────────────

type mockCallbackHandler struct{}

func (m *mockCallbackHandler) Handle(_ context.Context, _ CallbackEvent, _ map[string]interface{}) {}

// ── mockCallbackRegistry ────────────────────────────

type mockCallbackRegistry struct{}

func (m *mockCallbackRegistry) On(_ CallbackEvent, _ CallbackHandler) {}
func (m *mockCallbackRegistry) Remove(_ CallbackEvent) {}
func (m *mockCallbackRegistry) Emit(_ context.Context, _ CallbackEvent, _ map[string]interface{}) {}

// ── mockFlightRecorder ──────────────────────────────

type mockFlightRecorder struct{}

func (m *mockFlightRecorder) Record(_ context.Context, _ interface{}) error { return nil }
func (m *mockFlightRecorder) Replay(_ context.Context, _ string) (interface{}, error) { return nil, nil }
func (m *mockFlightRecorder) Stop() {}

// ── mockContextCleaner ──────────────────────────────

type mockContextCleaner struct{}

func (m *mockContextCleaner) Clean(_ []Message, _ ...CleanOptions) []Message { return nil }
func (m *mockContextCleaner) CleanWithTurns(_ []Message, _ ...CleanOptions) []Message { return nil }
func (m *mockContextCleaner) Stats() CleanerStats { return CleanerStats{} }
func (m *mockContextCleaner) ResetStats() {}

// ── mockMCPManager ──────────────────────────────────

type mockMCPManager struct{}

func (m *mockMCPManager) Start(_ context.Context) error { return nil }
func (m *mockMCPManager) Stop(_ context.Context) error { return nil }
func (m *mockMCPManager) ListServers() []MCPStatus { return nil }

// ── mockDashboard ───────────────────────────────────

type mockDashboard struct{}

func (m *mockDashboard) Handler() http.Handler { return nil }
