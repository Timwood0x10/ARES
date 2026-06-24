package client

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/api/core"
	agentSvc "github.com/Timwood0x10/ares/api/service/agent"
	llmSvc "github.com/Timwood0x10/ares/api/service/llm"
	memorySvc "github.com/Timwood0x10/ares/api/service/memory"
	retrievalSvc "github.com/Timwood0x10/ares/api/service/retrieval"
)

// TestHealthReport_Structure verifies HealthReport fields are zero-valued correctly.
func TestHealthReport_Structure(t *testing.T) {
	report := HealthReport{}
	require.False(t, report.OverallStatus)
	require.True(t, report.Timestamp.IsZero())
	require.False(t, report.LLMStatus.Available)
	require.False(t, report.MemoryStatus.Available)
}

// TestCheckServiceHealth_NilService tests that a nil service returns unavailable.
func TestCheckServiceHealth_NilService(t *testing.T) {
	status := checkServiceHealth("Test", nil)
	require.False(t, status.Available)
	require.Contains(t, status.Error, "Test")
}

// TestCheckServiceHealth_NonNilService tests that a non-nil service returns available.
func TestCheckServiceHealth_NonNilService(t *testing.T) {
	status := checkServiceHealth("Test", &struct{}{})
	require.True(t, status.Available)
	require.Empty(t, status.Error)
}

// TestCheckLLMHealth_NilService tests LLM health check with nil service.
func TestCheckLLMHealth_NilService(t *testing.T) {
	status := checkLLMHealth(context.Background(), nil)
	require.False(t, status.Available)
	require.Contains(t, status.Error, "not configured")
}

// TestBuildHealthReport_AllHealthy tests overall status when all services are healthy.
func TestBuildHealthReport_AllHealthy(t *testing.T) {
	report := buildHealthReport(
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
	)
	require.True(t, report.OverallStatus)
	require.False(t, report.Timestamp.IsZero())
}

// TestBuildHealthReport_OneUnhealthy tests overall status when one service is down.
func TestBuildHealthReport_OneUnhealthy(t *testing.T) {
	report := buildHealthReport(
		ServiceStatus{Available: true},
		ServiceStatus{Available: false, Error: "memory down"},
		ServiceStatus{Available: true},
		ServiceStatus{Available: true},
	)
	require.False(t, report.OverallStatus)
	require.Equal(t, "memory down", report.MemoryStatus.Error)
}

// TestClientHealth_EmptyConfig tests Health on client with no services configured.
func TestClientHealth_EmptyConfig(t *testing.T) {
	client, err := NewClient(&Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	report, err := client.Health(ctx)
	require.NoError(t, err)
	require.NotNil(t, report)

	// With no services configured, each service reports as unavailable
	require.False(t, report.OverallStatus)
	require.Contains(t, report.LLMStatus.Error, "not configured")
	require.Contains(t, report.MemoryStatus.Error, "not configured")
	require.Contains(t, report.RetrievalStatus.Error, "not configured")
	require.Contains(t, report.WorkflowStatus.Error, "not configured")
}

// TestClientHealth_WithServices tests Health with some services configured.
func TestClientHealth_WithServices(t *testing.T) {
	baseCfg := &core.BaseConfig{
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
	}
	client, err := NewClient(&Config{
		BaseConfig: baseCfg,
		Memory: &memorySvc.Config{
			BaseConfig: baseCfg,
			Repo:       memorySvc.NewMemoryRepository(),
		},
		LLM: &llmSvc.Config{
			BaseConfig: baseCfg,
			LLMConfig: &core.LLMConfig{
				Provider: core.LLMProviderOllama,
				BaseURL:  "http://localhost:11434",
				Model:    "llama3.2",
				Timeout:  60,
			},
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	report, err := client.Health(ctx)
	require.NoError(t, err)
	require.NotNil(t, report)

	// Memory should be available (non-nil)
	require.True(t, report.MemoryStatus.Available)

	// LLM availability depends on whether IsEnabled returns true
	// (it checks internal client state)
}

// TestClientConfig_ReturnsSnapshot tests Config() returns a deep copy, not the internal reference.
func TestClientConfig_ReturnsSnapshot(t *testing.T) {
	cfg := &Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 60 * time.Second,
			MaxRetries:     5,
		},
	}
	client, err := NewClient(cfg)
	require.NoError(t, err)

	got := client.Config()
	// FIX: Config() returns a deep copy, not the same pointer.
	// External mutation must not affect client's internal state.
	require.NotSame(t, cfg, got)
	require.Equal(t, cfg.BaseConfig.RequestTimeout, got.BaseConfig.RequestTimeout)

	// Mutating the copy must not affect the original.
	got.BaseConfig.RequestTimeout = 999 * time.Second
	require.Equal(t, 60*time.Second, client.Config().BaseConfig.RequestTimeout)
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
	require.NoError(t, err)

	ctx := context.Background()
	err = client.Close(ctx)
	require.NoError(t, err)

	err = client.Close(ctx)
	require.NoError(t, err)
}

// TestClientClose_WithServices tests Close with services configured.
func TestClientClose_WithServices(t *testing.T) {
	baseCfg := &core.BaseConfig{
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
	}
	client, err := NewClient(&Config{
		BaseConfig: baseCfg,
		Agent: &agentSvc.Config{
			BaseConfig: baseCfg,
			Repo:       agentSvc.NewMemoryRepository(),
		},
		Memory: &memorySvc.Config{
			BaseConfig: baseCfg,
			Repo:       memorySvc.NewMemoryRepository(),
		},
		Retrieval: &retrievalSvc.Config{
			BaseConfig: baseCfg,
			Repo:       retrievalSvc.NewMemoryRepository(),
		},
	})
	require.NoError(t, err)

	ctx := context.Background()
	err = client.Close(ctx)
	require.NoError(t, err)
}
