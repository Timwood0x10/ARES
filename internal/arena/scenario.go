package arena

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Scenario defines a chaos engineering scenario with rich configuration.
type Scenario struct {
	Name        string            `yaml:"name" json:"name"`                                   // required
	Description string            `yaml:"description,omitempty" json:"description,omitempty"` // optional
	Tags        []string          `yaml:"tags,omitempty" json:"tags,omitempty"`               // optional labels
	Config      ScenarioConfig    `yaml:"config,omitempty" json:"config,omitempty"`           // scenario-level config
	Actions     []ScheduledAction `yaml:"actions" json:"actions"`                             // required, at least 1
}

// ScenarioConfig holds scenario-level configuration.
type ScenarioConfig struct {
	StopOnError     bool          `yaml:"stop_on_error,omitempty" json:"stop_on_error,omitempty"`       // stop on first failure
	ParallelActions bool          `yaml:"parallel_actions,omitempty" json:"parallel_actions,omitempty"` // execute actions concurrently
	MaxConcurrent   int           `yaml:"max_concurrent,omitempty" json:"max_concurrent,omitempty"`     // max parallel actions (default 3)
	Timeout         time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`                   // overall scenario timeout
	Warmup          time.Duration `yaml:"warmup,omitempty" json:"warmup,omitempty"`                     // delay before first action
	Cooldown        time.Duration `yaml:"cooldown,omitempty" json:"cooldown,omitempty"`                 // delay after last action
}

// ScheduledAction pairs a delay with an action to execute.
type ScheduledAction struct {
	Delay         time.Duration `yaml:"delay" json:"delay"`                                       // delay before this action
	Action        Action        `yaml:"action" json:"action"`                                     // the action to execute
	Label         string        `yaml:"label,omitempty" json:"label,omitempty"`                   // human-readable label
	ExpectSuccess bool          `yaml:"expect_success,omitempty" json:"expect_success,omitempty"` // expected result for verification
	DependsOn     []string      `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`         // labels of actions this depends on
}

// ScenarioReport holds the results of a scenario execution.
type ScenarioReport struct {
	ScenarioName string          `json:"scenario_name"`
	Description  string          `json:"description,omitempty"`
	StartedAt    time.Time       `json:"started_at"`
	FinishedAt   time.Time       `json:"finished_at"`
	Duration     time.Duration   `json:"duration"`
	Results      []Result        `json:"results"`
	Passed       int             `json:"passed"`
	Failed       int             `json:"failed"`
	Skipped      int             `json:"skipped"`
	Score        ResilienceScore `json:"score"`
	Verified     bool            `json:"verified"` // all expect_success matched actual
}

// LoadScenario reads and parses a scenario from YAML data.
func LoadScenario(data []byte) (*Scenario, error) {
	var s Scenario
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("arena: parse scenario YAML: %w", err)
	}
	return &s, nil
}

// LoadScenarioFile reads a scenario from a file path.
func LoadScenarioFile(path string) (*Scenario, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("arena: read scenario file %s: %w", path, err)
	}
	return LoadScenario(data)
}

// ValidateScenario checks that a scenario is well-formed.
func ValidateScenario(s *Scenario) error {
	if s == nil {
		return fmt.Errorf("arena: scenario is nil")
	}
	if s.Name == "" {
		return fmt.Errorf("arena: scenario name is required")
	}
	if len(s.Actions) == 0 {
		return fmt.Errorf("arena: scenario must have at least one action")
	}
	for i, sa := range s.Actions {
		if sa.Delay < 0 {
			return fmt.Errorf("arena: action[%d] has negative delay: %v", i, sa.Delay)
		}
		if sa.Action.Type == "" {
			return fmt.Errorf("arena: action[%d] has empty type", i)
		}
		if err := ValidateAction(sa.Action); err != nil {
			return fmt.Errorf("arena: action[%d] validation failed: %w", i, err)
		}
	}
	if s.Config.MaxConcurrent < 0 {
		return fmt.Errorf("arena: config.max_concurrent must be non-negative")
	}
	if s.Config.Timeout < 0 {
		return fmt.Errorf("arena: config.timeout must be non-negative")
	}
	return nil
}

// RunScenarioReport executes a scenario and returns a full report.
// It supports warmup/cooldown delays, timeout context, and stop-on-error behavior.
func RunScenarioReport(ctx context.Context, service *Service, scenario Scenario) (*ScenarioReport, error) {
	if service == nil {
		return nil, fmt.Errorf("arena: service is nil")
	}
	if scenario.Name == "" {
		return nil, fmt.Errorf("arena: scenario name is empty")
	}

	report := &ScenarioReport{
		ScenarioName: scenario.Name,
		Description:  scenario.Description,
		StartedAt:    time.Now(),
	}

	// Apply overall timeout if configured.
	cfg := scenario.Config
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	slog.Info("arena: running scenario report",
		"name", scenario.Name,
		"actions", len(scenario.Actions),
		"warmup", cfg.Warmup,
		"timeout", cfg.Timeout,
	)

	// Warmup delay before first action.
	if cfg.Warmup > 0 {
		select {
		case <-ctx.Done():
			report.FinishedAt = time.Now()
			report.Duration = time.Since(report.StartedAt)
			return report, ctx.Err()
		case <-time.After(cfg.Warmup):
		}
	}

	results := make([]Result, 0, len(scenario.Actions))

	for i, sa := range scenario.Actions {
		// Apply per-action delay.
		if sa.Delay > 0 {
			select {
			case <-ctx.Done():
				slog.Warn("arena: scenario cancelled during delay",
					"name", scenario.Name,
					"step", i,
				)
				report.FinishedAt = time.Now()
				report.Duration = time.Since(report.StartedAt)
				report.Results = results
				report.computeTotals()
				report.Score = CalculateScoreV1(service.Stats(), service.calculateAvgRecoveryTime(nil))
				return report, ctx.Err()
			case <-time.After(sa.Delay):
			}
		}

		// Validate before execution.
		if err := ValidateAction(sa.Action); err != nil {
			result := Result{
				Success:  false,
				Action:   sa.Action,
				Error:    err.Error(),
				Duration: 0,
			}
			results = append(results, result)
			slog.Error("arena: scenario action validation failed",
				"name", scenario.Name,
				"step", i,
				"label", sa.Label,
				"error", err,
			)

			if cfg.StopOnError {
				break
			}
			continue
		}

		result := service.Execute(ctx, sa.Action)
		results = append(results, result)

		slog.Info("arena: scenario step completed",
			"name", scenario.Name,
			"step", i,
			"label", sa.Label,
			"type", sa.Action.Type,
			"success", result.Success,
		)

		// Stop on error if configured.
		if cfg.StopOnError && !result.Success {
			slog.Warn("arena: stopping scenario due to stop_on_error",
				"name", scenario.Name,
				"step", i,
			)
			break
		}
	}

	// Cooldown after all actions.
	if cfg.Cooldown > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(cfg.Cooldown):
		}
	}

	report.FinishedAt = time.Now()
	report.Duration = time.Since(report.StartedAt)
	report.Results = results
	report.computeTotals()
	report.Score = CalculateScoreV1(service.Stats(), service.calculateAvgRecoveryTime(nil))
	report.checkVerified(scenario)

	slog.Info("arena: scenario report completed",
		"name", scenario.Name,
		"passed", report.Passed,
		"failed", report.Failed,
		"duration", report.Duration,
		"verified", report.Verified,
	)

	return report, nil
}

// computeTotals fills in Passed/Failed/Skipped from Results.
func (r *ScenarioReport) computeTotals() {
	for _, res := range r.Results {
		if res.Success {
			r.Passed++
		} else {
			r.Failed++
		}
	}
}

// checkVerified compares ExpectSuccess against actual results.
func (r *ScenarioReport) checkVerified(scenario Scenario) {
	allMatch := true
	for i, res := range r.Results {
		if i < len(scenario.Actions) && scenario.Actions[i].ExpectSuccess {
			if res.Success != scenario.Actions[i].ExpectSuccess {
				allMatch = false
			}
		}
	}
	// If no actions have expect_success set, consider it verified by default.
	hasExpectations := false
	for _, sa := range scenario.Actions {
		if sa.ExpectSuccess {
			hasExpectations = true
			break
		}
	}
	r.Verified = !hasExpectations || allMatch
}

// RunScenario executes all actions in a scenario with the specified delays.
// Returns the results of all executed actions. Stops if the context is cancelled.
// NOTE: This function preserves backward compatibility. New code should use RunScenarioReport.
func RunScenario(ctx context.Context, service *Service, scenario Scenario) ([]Result, error) {
	if service == nil {
		return nil, fmt.Errorf("arena: service is nil")
	}
	if scenario.Name == "" {
		return nil, fmt.Errorf("arena: scenario name is empty")
	}

	slog.Info("arena: running scenario",
		"name", scenario.Name,
		"actions", len(scenario.Actions),
	)

	results := make([]Result, 0, len(scenario.Actions))

	for i, sa := range scenario.Actions {
		// Apply delay before executing the action.
		if sa.Delay > 0 {
			select {
			case <-ctx.Done():
				slog.Warn("arena: scenario cancelled during delay",
					"name", scenario.Name,
					"step", i,
				)
				return results, ctx.Err()
			case <-time.After(sa.Delay):
			}
		}

		// Validate the action before execution.
		if err := ValidateAction(sa.Action); err != nil {
			result := Result{
				Success:  false,
				Action:   sa.Action,
				Error:    err.Error(),
				Duration: 0,
			}
			results = append(results, result)
			slog.Error("arena: scenario action validation failed",
				"name", scenario.Name,
				"step", i,
				"error", err,
			)
			continue
		}

		result := service.Execute(ctx, sa.Action)
		results = append(results, result)

		slog.Info("arena: scenario step completed",
			"name", scenario.Name,
			"step", i,
			"type", sa.Action.Type,
			"success", result.Success,
		)
	}

	slog.Info("arena: scenario completed",
		"name", scenario.Name,
		"total", len(results),
	)

	return results, nil
}
