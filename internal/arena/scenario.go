package arena

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Scenario defines a sequence of chaos actions with delays between them.
type Scenario struct {
	Name    string            `yaml:"name"`
	Actions []ScheduledAction `yaml:"actions"`
}

// ScheduledAction pairs a delay with an action to execute.
type ScheduledAction struct {
	Delay  time.Duration `yaml:"delay"`
	Action Action        `yaml:"action"`
}

// RunScenario executes all actions in a scenario with the specified delays.
// Returns the results of all executed actions. Stops if the context is cancelled.
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
