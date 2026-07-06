// nolint: errcheck // Test code may ignore return values
package errors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultStrategiesLoaded(t *testing.T) {
	// Test that default strategies are loaded
	tests := []struct {
		name          string
		code          string
		expectedDLQ   bool
		expectedAlert bool
	}{
		{"Agent retry", "01-002", true, false},
		{"Agent panic", "01-003", true, true},
		{"Task queue full", "01-004", false, true},
		{"Heartbeat missed", "02-003", true, true},
		{"DB connection failed", "03-001", false, true},
		{"LLM request failed", "04-001", false, true},
		{"LLM quota exceeded", "04-003", false, true},
		{"LLM validation failed", "04-006", false, true},
		{"LLM auth failed", "04-007", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			strategy := GetStrategy(tt.code)
			if strategy.DLQEnabled != tt.expectedDLQ {
				t.Errorf("Expected DLQ=%v, got %v", tt.expectedDLQ, strategy.DLQEnabled)
			}
			if strategy.AlertEnabled != tt.expectedAlert {
				t.Errorf("Expected Alert=%v, got %v", tt.expectedAlert, strategy.AlertEnabled)
			}
		})
	}
}

func TestGetStrategyDefaultFallback(t *testing.T) {
	strategy := GetStrategy("non-existent-code")
	if strategy.MaxRetries != 1 {
		t.Errorf("Expected default MaxRetries=1, got %d", strategy.MaxRetries)
	}
	if strategy.Backoff != 1*time.Second {
		t.Errorf("Expected default Backoff=1s, got %v", strategy.Backoff)
	}
}

func TestSetStrategy(t *testing.T) {
	code := "test-001"
	expectedStrategy := ErrorStrategy{
		Backoff:      2 * time.Second,
		MaxRetries:   5,
		DLQEnabled:   true,
		AlertEnabled: true,
		AlertMessage: "Test message",
	}

	SetStrategy(code, expectedStrategy)
	strategy := GetStrategy(code)

	if strategy.Backoff != expectedStrategy.Backoff {
		t.Errorf("Expected Backoff=%v, got %v", expectedStrategy.Backoff, strategy.Backoff)
	}
	if strategy.MaxRetries != expectedStrategy.MaxRetries {
		t.Errorf("Expected MaxRetries=%d, got %d", expectedStrategy.MaxRetries, strategy.MaxRetries)
	}
	if strategy.DLQEnabled != expectedStrategy.DLQEnabled {
		t.Errorf("Expected DLQ=%v, got %v", expectedStrategy.DLQEnabled, strategy.DLQEnabled)
	}
	if strategy.AlertMessage != expectedStrategy.AlertMessage {
		t.Errorf("Expected AlertMessage=%s, got %s", expectedStrategy.AlertMessage, strategy.AlertMessage)
	}
}

func TestValidateStrategy(t *testing.T) {
	tests := []struct {
		name        string
		strategy    ErrorStrategy
		expectError bool
	}{
		{"Valid strategy", ErrorStrategy{Backoff: 1 * time.Second, MaxRetries: 3}, false},
		{"Negative MaxRetries", ErrorStrategy{Backoff: 1 * time.Second, MaxRetries: -1}, true},
		{"Negative Backoff", ErrorStrategy{Backoff: -1 * time.Second, MaxRetries: 3}, true},
		{"MaxRetries too high", ErrorStrategy{Backoff: 1 * time.Second, MaxRetries: 15}, true},
		{"Zero MaxRetries", ErrorStrategy{Backoff: 0, MaxRetries: 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStrategy(&tt.strategy)
			if (err != nil) != tt.expectError {
				t.Errorf("Expected error=%v, got %v", tt.expectError, err)
			}
		})
	}
}

func TestExportStrategiesToConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "error_strategies.json")

	err := ExportStrategiesToConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to export strategies: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Config file was not created")
	}

	// Verify we can load it back
	strategies := GetAllStrategies()
	if len(strategies) == 0 {
		t.Error("No strategies found in registry")
	}
}

func TestLoadStrategiesFromConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "custom_strategies.json")

	// Create custom config
	customConfig := ConfigFile{
		Version: "1.0",
		Strategies: map[string]ErrorStrategy{
			"custom-001": {
				Backoff:      10 * time.Second,
				MaxRetries:   5,
				DLQEnabled:   true,
				AlertEnabled: true,
				AlertMessage: "Custom error",
			},
		},
	}

	data, err := json.MarshalIndent(customConfig, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	// Load the config
	err = LoadStrategiesFromConfig(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Verify custom strategy is loaded
	strategy := GetStrategy("custom-001")
	if strategy.Backoff != 10*time.Second {
		t.Errorf("Expected Backoff=10s, got %v", strategy.Backoff)
	}
	if strategy.MaxRetries != 5 {
		t.Errorf("Expected MaxRetries=5, got %d", strategy.MaxRetries)
	}
}

func TestShouldDLQ(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected bool
	}{
		{"DLQ enabled", "01-002", true},
		{"DLQ disabled", "02-002", false},
		{"Unknown code", "unknown-999", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldDLQ(tt.code)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestShouldAlert(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected bool
	}{
		{"Alert enabled", "01-003", true},
		{"Alert disabled", "01-002", false},
		{"Unknown code", "unknown-999", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldAlert(tt.code)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGetAlertMessage(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		expectedMsg string
	}{
		{"Has alert message", "01-003", "Agent panic detected"},
		{"No alert message", "01-002", ""},
		{"Unknown code", "unknown-999", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := GetAlertMessage(tt.code)
			if msg != tt.expectedMsg {
				t.Errorf("Expected message '%s', got '%s'", tt.expectedMsg, msg)
			}
		})
	}
}

// nolint: errcheck // Test code may ignore return values
// nolint: errcheck // Test code may ignore return values
