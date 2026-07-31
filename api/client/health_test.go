package client

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// TestHealthReport_Structure verifies HealthReport fields are zero-valued correctly.
func TestHealthReport_Structure(t *testing.T) {
	report := HealthReport{}
	if report.Healthy {
		t.Errorf("expected Healthy to be false")
	}
	if !report.Timestamp.IsZero() {
		t.Errorf("expected Timestamp to be zero")
	}
	if report.LLMStatus.Available {
		t.Errorf("expected LLMStatus.Available to be false")
	}
	if report.MemoryStatus.Available {
		t.Errorf("expected MemoryStatus.Available to be false")
	}
}

// TestCheckServiceHealth_NilService tests that a nil service returns unavailable.
func TestCheckServiceHealth_NilService(t *testing.T) {
	status := checkServiceHealth("Test", nil)
	if status.Available {
		t.Errorf("expected Available to be false")
	}
	if status.Error != "Test service not configured" {
		t.Errorf("expected 'Test service not configured', got %q", status.Error)
	}
}

// TestCheckServiceHealth_NonNilService tests that a non-nil service returns available.
func TestCheckServiceHealth_NonNilService(t *testing.T) {
	status := checkServiceHealth("Test", &struct{}{})
	if !status.Available {
		t.Errorf("expected Available to be true")
	}
	if status.Error != "" {
		t.Errorf("expected empty error, got %q", status.Error)
	}
}

// TestCheckLLMHealth_NilService tests LLM health check with nil service.
func TestCheckLLMHealth_NilService(t *testing.T) {
	status := checkLLMHealth(context.Background(), nil)
	if status.Available {
		t.Errorf("expected Available to be false")
	}
	if status.Error != "LLM service not configured" {
		t.Errorf("expected 'LLM service not configured', got %q", status.Error)
	}
}

// TestCheckLLMHealth_DisabledService tests LLM health with a service whose IsEnabled returns false.
func TestCheckLLMHealth_DisabledService(t *testing.T) {
	svc := &stubLLMService{disabled: true}
	status := checkLLMHealth(context.Background(), svc)
	if status.Available {
		t.Errorf("expected Available to be false")
	}
	if status.Error != "LLM service not enabled" {
		t.Errorf("expected 'LLM service not enabled', got %q", status.Error)
	}
}

// TestCheckLLMHealth_EnabledService tests LLM health with a functional service.
func TestCheckLLMHealth_EnabledService(t *testing.T) {
	svc := &stubLLMService{}
	status := checkLLMHealth(context.Background(), svc)
	if !status.Available {
		t.Errorf("expected Available to be true")
	}
	if status.Error != "" {
		t.Errorf("expected empty error, got %q", status.Error)
	}
	if !status.Available {
		t.Errorf("expected Available to be true")
	}
}

// TestBuildHealthReport_AllHealthy tests overall status when all services are healthy.
func TestBuildHealthReport_AllHealthy(t *testing.T) {
	report := buildHealthReport(
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
	)
	if !report.Healthy {
		t.Errorf("expected Healthy to be true")
	}
	if report.Timestamp.IsZero() {
		t.Errorf("expected Timestamp to be non-zero")
	}
}

// TestBuildHealthReport_OneUnhealthy tests overall status when one service is down.
func TestBuildHealthReport_OneUnhealthy(t *testing.T) {
	report := buildHealthReport(
		ServiceStatus{Available: true},
		ServiceStatus{Available: false, Error: "memory down"},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
	)
	if report.Healthy {
		t.Errorf("expected Healthy to be false")
	}
	if report.MemoryStatus.Error != "memory down" {
		t.Errorf("expected 'memory down', got %q", report.MemoryStatus.Error)
	}
}

// TestBuildHealthReport_UnconfiguredSkipsService tests that buildHealthReport
// includes services with an error in the configured list, so overall status
// reflects their unavailability.
func TestBuildHealthReport_UnconfiguredSkipsService(t *testing.T) {
	report := buildHealthReport(
		ServiceStatus{Available: false, Error: "not configured"},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
	)
	// A service with non-empty Error is included in the configured list.
	// If it's not available, overall status is false.
	if report.Healthy {
		t.Errorf("expected Healthy to be false when a configured service is unavailable")
	}
}

// TestClientHealth_ReportsHealthy tests the Health method returns overall status based on closed state.
func TestClientHealth_ReportsHealthy(t *testing.T) {
	client, err := NewClient(&Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	report, err := client.Health(ctx)
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}

	// Client not closed, no services configured => Healthy is true.
	if !report.Healthy {
		t.Errorf("expected Healthy to be true when client is open")
	}
	if report.Timestamp.IsZero() {
		t.Errorf("expected Timestamp to be set")
	}

	// After closing, Healthy should be false
	_ = client.Close(ctx)
	report, err = client.Health(ctx)
	if err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if report.Healthy {
		t.Errorf("expected Healthy to be false after Close()")
	}
}

// TestClientHealth_ProbesLLMService verifies Health actually invokes
// checkLLMHealth on the configured LLM service (previously checkLLMHealth had
// no production caller). An enabled LLM service reports Available; a disabled
// one reports unavailable and makes the overall report unhealthy.
func TestClientHealth_ProbesLLMService(t *testing.T) {
	ctx := context.Background()

	t.Run("enabled LLM service is reported available", func(t *testing.T) {
		client, err := NewClient(&Config{
			BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
			LLM:        &stubLLMService{},
		})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}

		report, err := client.Health(ctx)
		if err != nil {
			t.Fatalf("Health() error = %v", err)
		}
		if !report.LLMStatus.Available {
			t.Errorf("expected LLMStatus.Available to be true for an enabled service")
		}
		if report.LLMStatus.Error != "" {
			t.Errorf("expected empty LLM error, got %q", report.LLMStatus.Error)
		}
		if !report.Healthy {
			t.Errorf("expected Healthy to be true when the only configured service is available")
		}
	})

	t.Run("disabled LLM service makes report unhealthy", func(t *testing.T) {
		client, err := NewClient(&Config{
			BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
			LLM:        &stubLLMService{disabled: true},
		})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}

		report, err := client.Health(ctx)
		if err != nil {
			t.Fatalf("Health() error = %v", err)
		}
		if report.LLMStatus.Available {
			t.Errorf("expected LLMStatus.Available to be false for a disabled service")
		}
		if report.LLMStatus.Error == "" {
			t.Errorf("expected a non-empty LLM error for a disabled service")
		}
		if report.Healthy {
			t.Errorf("expected Healthy to be false when a configured service is unavailable")
		}
	})

	t.Run("unconfigured LLM service is omitted from Healthy", func(t *testing.T) {
		client, err := NewClient(&Config{
			BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
		})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}

		report, err := client.Health(ctx)
		if err != nil {
			t.Fatalf("Health() error = %v", err)
		}
		// No LLM configured: status stays zero (not available, no error) and
		// does not make the report unhealthy.
		if report.LLMStatus.Available {
			t.Errorf("expected LLMStatus.Available to be false when LLM is not configured")
		}
		if report.LLMStatus.Error != "" {
			t.Errorf("expected empty LLM error when LLM is not configured, got %q", report.LLMStatus.Error)
		}
		if !report.Healthy {
			t.Errorf("expected Healthy to be true when no services are configured and client is open")
		}
	})
}

// TestClientConfig_ReturnsInternalPointer tests Config() returns the config pointer directly.
func TestClientConfig_ReturnsInternalPointer(t *testing.T) {
	cfg := &Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 60 * time.Second,
			MaxRetries:     5,
		},
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	got := client.Config()
	// Config() returns the internal pointer directly (not a copy)
	if got != cfg {
		t.Errorf("expected Config() to return the same pointer")
	}
	if got.BaseConfig.RequestTimeout != cfg.BaseConfig.RequestTimeout {
		t.Errorf("expected RequestTimeout %v, got %v", cfg.BaseConfig.RequestTimeout, got.BaseConfig.RequestTimeout)
	}
}

// TestClientClose_Idempotent tests that calling Close twice is safe.
func TestClientClose_Idempotent(t *testing.T) {
	client, err := NewClient(&Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	if err := client.Close(ctx); err != nil {
		t.Errorf("first Close() error = %v", err)
	}
	if err := client.Close(ctx); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

// TestClientClose_WithServices tests Close with services configured.
func TestClientClose_WithServices(t *testing.T) {
	client, err := NewClient(&Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
		Agent:     &stubAgentService{},
		Memory:    &stubMemoryService{},
		Retrieval: &stubRetrievalService{},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	if err := client.Close(ctx); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
