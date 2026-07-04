package planner

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/logger"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// ToolExecutionBridge wraps a tool registry with planner-based fallback.
//
// When a tool call arrives (from LLM or direct invocation):
//  1. Try the named tool directly.
//  2. If not found, use the CapabilityPlanner to resolve the user's
//     intent and select the best tool automatically.
//
// This bridge ensures backward compatibility: existing LLM tool-name
// execution continues to work unchanged, while the planner fallback
// handles cases where the LLM does not specify a valid tool name.
type ToolExecutionBridge struct {
	registry *core.Registry
	planner  *Planner
}

// NewToolExecutionBridge creates a bridge with planner fallback.
// Returns error if registry or planner is nil.
func NewToolExecutionBridge(registry *core.Registry, planner *Planner) (*ToolExecutionBridge, error) {
	if registry == nil {
		return nil, fmt.Errorf("tool_bridge: registry is nil")
	}
	if planner == nil {
		return nil, fmt.Errorf("tool_bridge: planner is nil")
	}
	return &ToolExecutionBridge{
		registry: registry,
		planner:  planner,
	}, nil
}

// Execute runs a tool by name with fallback to planner resolution.
//
// Args:
//
//	ctx - cancellation and timeout context.
//	toolName - tool name from LLM (may be empty for planner-only).
//	params - parameters to pass to the tool.
//	userRequest - original user request for planner fallback.
//
// Returns:
//
//	result - tool execution result.
//	err - error if all resolution paths fail.
func (b *ToolExecutionBridge) Execute(
	ctx context.Context,
	toolName string,
	params map[string]interface{},
	userRequest string,
) (core.Result, error) {
	log := logger.Module("tool_bridge")

	// Path 1: Try the explicitly named tool.
	if toolName != "" {
		tool, exists := b.registry.Get(toolName)
		if exists {
			start := time.Now()
			result, err := tool.Execute(ctx, params)
			latency := time.Since(start)

			log.Info("tool_bridge: direct execution",
				"tool", toolName,
				"success", err == nil && result.Success,
				"latency_ms", latency.Milliseconds(),
			)
			return result, err
		}

		log.Warn("tool_bridge: tool not found, triggering planner fallback",
			"tool", toolName,
		)
	}

	// Path 2: Planner fallback — resolve intent and pick the best tool.
	if userRequest == "" {
		return core.Result{}, fmt.Errorf("tool_bridge: tool %q not found and no user request for fallback", toolName)
	}

	plan, pErr := b.planner.Plan(ctx, userRequest)
	if pErr != nil {
		return core.Result{}, fmt.Errorf("tool_bridge: planner fallback failed: %w", pErr)
	}

	if len(plan.Steps) == 0 {
		return core.Result{}, fmt.Errorf("tool_bridge: planner produced zero steps for %q", userRequest)
	}

	// Execute the first step of the plan.
	step := plan.Steps[0]
	tool, exists := b.registry.Get(step.ToolName)
	if !exists {
		return core.Result{}, fmt.Errorf("tool_bridge: planner selected tool %q but it is not registered", step.ToolName)
	}

	// Merge user params with plan params (user params win).
	mergedParams := make(map[string]interface{})
	for k, v := range step.Parameters {
		mergedParams[k] = v
	}
	for k, v := range params {
		mergedParams[k] = v
	}

	start := time.Now()
	result, err := tool.Execute(ctx, mergedParams)
	latency := time.Since(start)

	log.Info("tool_bridge: planner fallback execution",
		"tool", step.ToolName,
		"plan_id", plan.PlanID,
		"success", err == nil && result.Success,
		"latency_ms", latency.Milliseconds(),
	)

	return result, err
}
