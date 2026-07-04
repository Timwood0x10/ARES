// Package planner implements a capability-driven tool selection and execution
// planning layer. See types.go for the full pipeline description.
package planner

import (
	"context"
	"fmt"
)

// Planner is the top-level orchestrator that runs the full planning pipeline:
//
//  1. SemanticAnalyzer  — parse user request into Intent
//  2. CapabilityPlanner — decompose Intent into CapabilityRequirements
//  3. ToolResolver      — map requirements to ToolCandidates
//  4. ToolScorer        — rank candidates by static metadata + evidence
//  5. ExecutionPlanner  — build ExecutionPlan (single-step or DAG)
//
// Planner does NOT execute tools. It only produces plans.
type Planner struct {
	analyzer  SemanticAnalyzer
	planner   CapabilityPlanner
	resolver  ToolResolver
	scorer    ToolScorer
	extractor *ParameterExtractor
	execPlan  ExecutionPlanner
	evidence  EvidenceStore
}

// NewPlanner creates a fully wired planner with all default components.
// Returns error if any required dependency is nil.
func NewPlanner(
	analyzer SemanticAnalyzer,
	plannerImpl CapabilityPlanner,
	resolver ToolResolver,
	scorer ToolScorer,
	execPlan ExecutionPlanner,
	evidence EvidenceStore,
) (*Planner, error) {
	if analyzer == nil {
		return nil, fmt.Errorf("planner: SemanticAnalyzer is nil")
	}
	if plannerImpl == nil {
		return nil, fmt.Errorf("planner: CapabilityPlanner is nil")
	}
	if resolver == nil {
		return nil, fmt.Errorf("planner: ToolResolver is nil")
	}
	if scorer == nil {
		return nil, fmt.Errorf("planner: ToolScorer is nil")
	}
	if execPlan == nil {
		return nil, fmt.Errorf("planner: ExecutionPlanner is nil")
	}
	if evidence == nil {
		return nil, fmt.Errorf("planner: EvidenceStore is nil")
	}
	return &Planner{
		analyzer:  analyzer,
		planner:   plannerImpl,
		resolver:  resolver,
		scorer:    scorer,
		extractor: NewParameterExtractor(),
		execPlan:  execPlan,
		evidence:  evidence,
	}, nil
}

// Plan runs the full pipeline: analyze → plan capabilities → resolve → score → execute plan.
//
// Args:
//
//	ctx - cancellation and timeout context.
//	request - raw user request string.
//
// Returns:
//
//	plan - the execution plan (single-step or DAG).
//	err - error if any stage fails.
func (p *Planner) Plan(ctx context.Context, request string) (*ExecutionPlan, error) {
	// Step 1: Analyze
	intent, err := p.analyzer.Analyze(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("planner: analyze: %w", err)
	}

	// Step 2: Plan capabilities
	requirements, err := p.planner.Plan(ctx, intent)
	if err != nil {
		return nil, fmt.Errorf("planner: capability plan: %w", err)
	}

	// Step 3: Resolve each requirement to tool candidates
	for i, req := range requirements {
		candidates, err := p.resolver.Resolve(ctx, &req)
		if err != nil {
			return nil, fmt.Errorf("planner: resolve %q: %w", req.Name, err)
		}

		// Step 4: Score candidates with evidence
		evidence, qErr := p.evidence.Query(ctx, "", req.Name, 50)
		if qErr != nil {
			// Non-fatal: scoring can proceed without evidence.
			evidence = nil
		}

		scored, sErr := p.scorer.Score(ctx, candidates, evidence)
		if sErr != nil {
			return nil, fmt.Errorf("planner: score %q: %w", req.Name, sErr)
		}

		if len(scored) == 0 {
			return nil, fmt.Errorf("planner: no scored candidates for capability %q", req.Name)
		}

		// Pick the best candidate for this requirement.
		best := scored[0]
		requirements[i].ResolvedTool = best.ToolName
	}

	// Step 5: Build execution plan
	plan, err := p.execPlan.Plan(ctx, intent, requirements)
	if err != nil {
		return nil, fmt.Errorf("planner: execution plan: %w", err)
	}

	// Assign tool names to steps from resolved candidates.
	for i := range plan.Steps {
		if i < len(requirements) {
			plan.Steps[i].ToolName = requirements[i].ResolvedTool
		}
	}

	return plan, nil
}
