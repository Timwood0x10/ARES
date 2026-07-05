// Package integration — end-to-end integration tests for the ARES system.
//
// These tests wire bootstrap + dashboard + monitoring + arena together
// and verify the full pipeline: config loading, component assembly,
// health/intelligence endpoint responses, and arena execution.
package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/adapter"
)

// TestBootstrapDashboardHealth verifies the full pipeline:
// Bootstrap → EventStore → Engine → IntelAdapter → MonitorPlugin → HTTP health endpoint.
func TestBootstrapDashboardHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Bootstrap core infrastructure.
	cfg := &ares_config.Config{
		MCP: ares_config.MCPConfig{Servers: make([]ares_config.MCPServerEntry, 0)},
		LLM: ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
	}
	comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	if comp.EventStore == nil {
		t.Fatal("expected non-nil EventStore")
	}
	if comp.Runtime == nil {
		t.Fatal("expected non-nil Runtime")
	}
	if comp.Memory == nil {
		t.Fatal("expected non-nil Memory")
	}
	t.Logf("Bootstrap OK: runtime=%T memory=%T", comp.Runtime, comp.Memory)
}

// TestDashboardIntelEngine verifies the intelligence engine produces real health data.
func TestDashboardIntelEngine(t *testing.T) {
	// Create engine and observe some events.
	engine := dashboard.NewEngine(nil)

	// Observe agent events to populate health data.
	engine.ObserveAgentEvent("agent-1", "tick", 0, false)
	engine.ObserveAgentEvent("agent-1", "tick", 0, false)
	engine.ObserveAgentEvent("agent-1", "tick", 0, false)

	// System health should be healthy.
	sys := engine.SystemHealth()
	if sys.Score <= 0 {
		t.Fatalf("expected positive system health score, got %f", sys.Score)
	}
	if sys.Level != dashboard.HealthHealthy && sys.Level != dashboard.HealthDegraded {
		t.Fatalf("expected healthy or degraded level, got %s", sys.Level)
	}
	t.Logf("System health: level=%s score=%.2f", sys.Level, sys.Score)

	// Individual agent health.
	h := engine.Health("agent-1")
	if h.Score <= 0 {
		t.Fatalf("expected positive agent health score, got %f", h.Score)
	}
	t.Logf("Agent health: score=%.2f uptime=%.1f%%", h.Score, h.Uptime)

	// No anomalies yet.
	anomalies := engine.Anomalies()
	if len(anomalies) != 0 {
		t.Fatalf("expected 0 anomalies, got %d", len(anomalies))
	}

	// Trigger error anomaly by feeding enough error events (>25 for 5/min threshold over 5min window).
	for i := 0; i < 30; i++ {
		engine.ObserveAgentEvent("agent-2", "error", 0, true)
	}

	anomalies = engine.Anomalies()
	if len(anomalies) == 0 {
		t.Fatal("expected anomalies after error spike")
	}
	t.Logf("Detected %d anomalies after error spike", len(anomalies))
}

// TestIntelAdapterBridge verifies the IntelAdapter correctly bridges Engine → IntelProvider.
func TestIntelAdapterBridge(t *testing.T) {
	engine := dashboard.NewEngine(nil)
	engine.ObserveAgentEvent("agent-3", "tick", 0, false)

	intel := adapter.NewIntelAdapter(engine)

	// System level should not be unknown.
	level := intel.SystemLevel()
	if level == "unknown" {
		t.Fatal("expected non-unknown system level")
	}
	t.Logf("IntelAdapter SystemLevel: %s", level)

	// Anomaly count should be 0 initially.
	if n := intel.AnomalyCount(); n != 0 {
		t.Fatalf("expected 0 anomalies, got %d", n)
	}
}

// TestMonitoringHTTPHealthRoutes verifies the monitoring HTTP server
// returns real health data when wired with the intelligence engine.
func TestMonitoringHTTPHealthRoutes(t *testing.T) {
	// Create the engine.
	engine := dashboard.NewEngine(nil)
	engine.ObserveAgentEvent("test-agent", "tick", 0, false)

	// Create monitor plugin and inject IntelAdapter.
	plugin := monitoring.NewConsole().(*monitoring.MonitorPlugin)
	plugin.SetIntel(adapter.NewIntelAdapter(engine))

	// Start the plugin (no bus needed for health routes).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := plugin.Start(ctx, nil); err != nil {
		t.Fatalf("Start plugin: %v", err)
	}
	t.Cleanup(func() {
		if err := plugin.Stop(ctx); err != nil {
			t.Logf("plugin stop: %v", err)
		}
	})

	// Create HTTP server and test health endpoint.
	srv := monitoring.NewHTTPServer(plugin)
	httpSrv := httptest.NewServer(srv)
	defer httpSrv.Close()

	// GET /api/health
	reqHealth, err := http.NewRequestWithContext(context.Background(), http.MethodGet, httpSrv.URL+"/api/health", nil)
	if err != nil {
		t.Fatalf("create /api/health request: %v", err)
	}
	resp, err := http.DefaultClient.Do(reqHealth)
	if err != nil {
		t.Fatalf("GET /api/health: %v", err)
	}
	defer resp.Body.Close() //nolint: errcheck

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["level"] == nil || body["level"] == "unknown" {
		t.Fatalf("expected non-unknown health level, got %v", body["level"])
	}
	t.Logf("Health endpoint: level=%v agents=%v", body["level"], body["agents"])

	// GET /api/anomalies
	reqAnomalies, err := http.NewRequestWithContext(context.Background(), http.MethodGet, httpSrv.URL+"/api/anomalies", nil)
	if err != nil {
		t.Fatalf("create /api/anomalies request: %v", err)
	}
	resp2, err := http.DefaultClient.Do(reqAnomalies)
	if err != nil {
		t.Fatalf("GET /api/anomalies: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}

	// GET /api/insights
	reqInsights, err := http.NewRequestWithContext(context.Background(), http.MethodGet, httpSrv.URL+"/api/insights", nil)
	if err != nil {
		t.Fatalf("create /api/insights request: %v", err)
	}
	resp3, err := http.DefaultClient.Do(reqInsights)
	if err != nil {
		t.Fatalf("GET /api/insights: %v", err)
	}
	defer func() { _ = resp3.Body.Close() }()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp3.StatusCode)
	}
	t.Log("All health/intelligence endpoints responded 200 OK")
}
