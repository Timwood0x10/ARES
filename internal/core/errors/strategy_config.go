package errors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"goagentx/internal/errors"
)

// ConfigFile defines the structure for error strategy configuration file.
type ConfigFile struct {
	Version    string                   `json:"version"`
	Strategies map[string]ErrorStrategy `json:"strategies"`
}

// DefaultErrorStrategiesConfig provides the default error strategies as a config file.
var DefaultErrorStrategiesConfig = ConfigFile{
	Version: "1.0",
	Strategies: map[string]ErrorStrategy{
		// Agent module strategies
		"01-002": {Backoff: 5 * time.Second, MaxRetries: 3, DLQEnabled: true, AlertEnabled: false},
		"01-003": {Backoff: 10 * time.Second, MaxRetries: 2, DLQEnabled: true, AlertEnabled: true, AlertMessage: "Agent panic detected"},
		"01-004": {Backoff: 1 * time.Second, MaxRetries: 5, DLQEnabled: false, AlertEnabled: true, AlertMessage: "Task queue full"},

		// Protocol module strategies
		"02-002": {Backoff: 3 * time.Second, MaxRetries: 3, DLQEnabled: false, AlertEnabled: false},
		"02-003": {Backoff: 5 * time.Second, MaxRetries: 5, DLQEnabled: true, AlertEnabled: true, AlertMessage: "Heartbeat missed"},

		// Storage module strategies
		"03-001": {Backoff: 2 * time.Second, MaxRetries: 3, DLQEnabled: false, AlertEnabled: true, AlertMessage: "DB connection failed"},
		"03-002": {Backoff: 1 * time.Second, MaxRetries: 2, DLQEnabled: false, AlertEnabled: false},
		"03-003": {Backoff: 2 * time.Second, MaxRetries: 2, DLQEnabled: false, AlertEnabled: false},

		// LLM module strategies
		"04-001": {Backoff: 3 * time.Second, MaxRetries: 3, DLQEnabled: false, AlertEnabled: true, AlertMessage: "LLM request failed"},
		"04-002": {Backoff: 5 * time.Second, MaxRetries: 2, DLQEnabled: false, AlertEnabled: false},
		"04-003": {Backoff: 0, MaxRetries: 0, DLQEnabled: false, AlertEnabled: true, AlertMessage: "LLM quota exceeded"},
		"04-005": {Backoff: 1 * time.Second, MaxRetries: 3, DLQEnabled: false, AlertEnabled: false},
		"04-006": {Backoff: 0, MaxRetries: 0, DLQEnabled: false, AlertEnabled: true, AlertMessage: "LLM validation failed"},
		"04-007": {Backoff: 0, MaxRetries: 0, DLQEnabled: false, AlertEnabled: true, AlertMessage: "LLM authentication failed (401) - check API key"},
	},
}

// DefaultStrategy is the fallback strategy when no specific strategy is found.
var DefaultStrategy = ErrorStrategy{
	Backoff:      1 * time.Second,
	MaxRetries:   1,
	DLQEnabled:   false,
	AlertEnabled: false,
}

// strategyRegistry holds the error strategies with thread-safe access.
type strategyRegistry struct {
	mu         sync.RWMutex
	strategies map[string]ErrorStrategy
	allowedDir string
}

var globalRegistry = &strategyRegistry{
	strategies: make(map[string]ErrorStrategy),
}

// init initializes the registry with default strategies.
func init() {
	// Load default strategies
	for code, strategy := range DefaultErrorStrategiesConfig.Strategies {
		globalRegistry.strategies[code] = strategy
	}
}

// SetAllowedDir sets the allowed directory for config file loading.
// This is a security measure to prevent path traversal attacks.
func SetAllowedDir(dir string) {
	globalRegistry.allowedDir = dir
}

// LoadStrategiesFromConfig loads error strategies from a configuration file.
// Supported formats: JSON
func LoadStrategiesFromConfig(configPath string) error {
	// Security: validate path is within allowed directory
	if globalRegistry.allowedDir != "" {
		absPath, err := filepath.Abs(configPath)
		if err != nil {
			return fmt.Errorf("failed to get absolute path: %w", err)
		}
		absDir, err := filepath.Abs(globalRegistry.allowedDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute directory: %w", err)
		}
		if !strings.HasPrefix(absPath, absDir) {
			return fmt.Errorf("config path %s is outside allowed directory %s", configPath, globalRegistry.allowedDir)
		}
	}

	data, err := os.ReadFile(configPath) // #nosec G304
	if err != nil {
		return err
	}

	var config ConfigFile
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()

	// Merge with existing strategies (new configs override defaults)
	for code, strategy := range config.Strategies {
		if err := validateStrategy(&strategy); err != nil {
			return err
		}
		globalRegistry.strategies[code] = strategy
	}

	return nil
}

// validateStrategy validates an error strategy configuration.
func validateStrategy(strategy *ErrorStrategy) error {
	if strategy.MaxRetries < 0 {
		return &InvalidStrategyError{
			Field:   "MaxRetries",
			Message: "MaxRetries cannot be negative",
		}
	}

	if strategy.Backoff < 0 {
		return &InvalidStrategyError{
			Field:   "Backoff",
			Message: "Backoff cannot be negative",
		}
	}

	if strategy.MaxRetries > 10 {
		return &InvalidStrategyError{
			Field:   "MaxRetries",
			Message: "MaxRetries should not exceed 10",
		}
	}

	return nil
}

// SetStrategy sets or updates an error strategy for a specific error code.
//
// Args:
// code - error code identifier.
// strategy - error strategy configuration.
// Returns error if strategy validation fails.
func SetStrategy(code string, strategy ErrorStrategy) error {
	if err := validateStrategy(&strategy); err != nil {
		return errors.Wrapf(err, "invalid strategy for code %s", code)
	}

	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.strategies[code] = strategy
	return nil
}

// GetStrategy returns the error strategy for the given error code.
// Returns default strategy if code not found.
func GetStrategy(code string) ErrorStrategy {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	if strategy, ok := globalRegistry.strategies[code]; ok {
		return strategy
	}
	return DefaultStrategy
}

// GetAllStrategies returns a copy of all strategies.
func GetAllStrategies() map[string]ErrorStrategy {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	strategies := make(map[string]ErrorStrategy, len(globalRegistry.strategies))
	for code, strategy := range globalRegistry.strategies {
		strategies[code] = strategy
	}
	return strategies
}

// InvalidStrategyError is returned when an error strategy configuration is invalid.
type InvalidStrategyError struct {
	Field   string
	Message string
}

func (e *InvalidStrategyError) Error() string {
	return "invalid strategy: " + e.Field + " - " + e.Message
}

// ExportStrategiesToConfig exports current strategies to a configuration file.
func ExportStrategiesToConfig(configPath string) error {
	config := ConfigFile{
		Version:    "1.0",
		Strategies: GetAllStrategies(),
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0600)
}
