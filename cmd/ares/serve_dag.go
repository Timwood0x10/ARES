package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// buildLeaderLiveDAG constructs the leader's real workflow DAG from the
// configured sub-agents: input (leader) → one step per sub-agent → output
// (leader). This replaces the bootstrap synthetic 3-step placeholder so
// workflow/scheduler/recovery evolution patches hit the actual agent topology
// (F04, Stage 8).
//
// Args:
// cfg - fully resolved serve configuration; cfg.Agents.Sub is read.
//
// Returns:
// dag - the live MutableDAG, nil on error.
// err - error if a step ID is empty/duplicate or dependencies are invalid.
func buildLeaderLiveDAG(cfg *ares_config.Config) (*engine.MutableDAG, error) {
	steps := []*engine.Step{
		{ID: "input", Name: "Input", AgentType: cfg.Agents.Leader.ID, Input: "parse input"},
	}
	subIDs := make([]string, 0, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		stepID := strings.TrimSpace(s.ID)
		if stepID == "" {
			stepID = strings.TrimSpace(s.Type)
		}
		if stepID == "" {
			// Fail-loud instead of silently registering a broken DAG edge.
			return nil, fmt.Errorf("sub-agent has empty ID and empty type")
		}
		steps = append(steps, &engine.Step{
			ID:        stepID,
			Name:      s.Type,
			AgentType: s.Type,
			Input:     stepID,
			DependsOn: []string{"input"},
		})
		subIDs = append(subIDs, stepID)
	}
	// Output step depends on every sub-agent step (or just input when none).
	outputDeps := append([]string{"input"}, subIDs...)
	steps = append(steps, &engine.Step{
		ID:        "output",
		Name:      "Output",
		AgentType: cfg.Agents.Leader.ID,
		Input:     "format",
		DependsOn: outputDeps,
	})
	return engine.NewMutableDAG(steps)
}

// wireEvolutionLiveDAGs injects the live agent DAGs into the evolution
// system's executors, replacing the synthetic placeholder DAG created at
// bootstrap time. This ensures workflow/scheduler/recovery patches hit real
// runtime state. Extracted from runServe to keep its cyclomatic complexity
// within lint limits.
func wireEvolutionLiveDAGs(comp *ares_bootstrap.Components, mgr *ares_runtime.Manager, leaderID string) {
	if comp.NewEvolution == nil {
		return
	}
	for _, id := range []string{leaderID} {
		dag, ok := mgr.GetAgentDAG(id)
		if !ok || dag == nil {
			// Fail-loud: no live DAG is registered for this agent (the live
			// DAG supply chain is Track C, deferred), so workflow/scheduler/
			// recovery patches still hit synthetic executors. The warning is
			// expected on every startup until a live DAG is wired.
			log.Printf("serve: live DAG not registered for agent %q before Start; "+
				"workflow patches will hit synthetic executors (F04 gap, Track C deferred)", id)
			continue
		}
		liveDAG, dagOk := dag.(*engine.MutableDAG)
		if !dagOk {
			continue
		}
		// Register a LiveDAGPatchExecutor that directly mutates the agent's
		// live MutableDAG instead of a private noop graph.
		liveExec := newLiveDAGPatchExecutor(mgr, id)
		// Register as component AND as fallback so workflow structure patches
		// (insert/remove nodes/edges) with dynamic node ID targets are routed
		// to the live DAG executor.
		if err := comp.NewEvolution.PatchReg.RegisterComponent(liveExec); err != nil {
			log.Printf("serve: register live exec component: %v", err)
		}
		if err := comp.NewEvolution.PatchReg.Register("graph.scheduler", liveExec); err != nil {
			log.Printf("serve: register live exec graph.scheduler: %v", err)
		}
		comp.NewEvolution.PatchReg.SetFallback(liveExec)

		// Also update the existing graph executor for consistency.
		if err := comp.NewEvolution.UpdateLiveDAG(liveDAG); err != nil {
			log.Printf("serve: update live DAG failed: agent_id=%s error=%v", id, err)
		}

		// Update the WorkflowGenome's DAG reference so its evolution mutations
		// are based on the agent's real workflow topology instead of the
		// bootstrap 3-step placeholder. Without this, the genome generates
		// patches against the toy structure, so the content being evolved is
		// disconnected from reality.
		wfGenome, gErr := comp.NewEvolution.GenomeReg.Get("workflow")
		if gErr != nil {
			continue
		}
		setter, ok := wfGenome.(interface{ SetDAG(*engine.MutableDAG) })
		if !ok {
			continue
		}
		setter.SetDAG(liveDAG)
		log.Printf("serve: WorkflowGenome updated with live DAG for agent %s (%d steps)", id, len(liveDAG.Steps()))
	}
	// Replace the evolution system's isolated KnowledgeRuntime with the
	// agent's live KnowledgeRuntime. This ensures knowledge genome patches
	// (ChangeBudget/ChangePlanner/ChangeReducer) affect the actual runtime
	// used by the agent's knowledge tools, not the bootstrap placeholder.
	comp.NewEvolution.UpdateLiveKnowledgeRuntime(comp.KnowledgeRuntime)
}

// akfToolAdapter adapts an AKF MCP tool (func(ctx, input string) -> string)
// to the core_tools.Tool interface so it can be registered in the internal
// tool registry and used by sub-agents through the ToolBinder. This is the
// wiring that makes knowledge genome patches affect the agent's knowledge
// tools — because both share the same comp.KnowledgeRuntime instance.
type akfToolAdapter struct {
	name string
	desc string
	fn   func(ctx context.Context, input string) (string, error)
}

func (a *akfToolAdapter) Name() string                      { return a.name }
func (a *akfToolAdapter) Description() string               { return a.desc }
func (a *akfToolAdapter) Category() core_tools.ToolCategory { return core_tools.CategoryKnowledge }
func (a *akfToolAdapter) Capabilities() []core_tools.Capability {
	return []core_tools.Capability{core_tools.CapabilityKnowledge}
}
func (a *akfToolAdapter) Parameters() *core_tools.ParameterSchema { return nil }
func (a *akfToolAdapter) Execute(ctx context.Context, params map[string]interface{}) (core_tools.Result, error) {
	input, _ := params["input"].(string)
	if input == "" {
		// Serialize the whole params map as JSON input.
		b, _ := json.Marshal(params)
		input = string(b)
	}
	out, err := a.fn(ctx, input)
	if err != nil {
		return core_tools.NewErrorResult(err.Error()), nil
	}
	return core_tools.NewResult(true, map[string]interface{}{"output": out}), nil
}

// liveDAGPatchExecutor applies workflow structure patches directly to the
// agent's live engine.MutableDAG held by the runtime manager. Unlike the
// synthetic GraphPatchExecutor (which operates on a private noop *wfgraph.Graph),
// this executor reads the live DAG from the manager's dagStore, applies the
// mutation, and writes it back — so genome evolution patches to workflow
// structure (insert/remove nodes/edges) actually change the DAG the agent
// reads at runtime.
type liveDAGPatchExecutor struct {
	mgr     *ares_runtime.Manager
	agentID string
}

// errNoSnapshot is returned by Snapshot to signal that this executor does
// not produce a serializable snapshot. Callers should treat it as "no diff
// available" rather than a real failure.
var errNoSnapshot = errors.New("live DAG executor: snapshot not supported")

// errNoRollback is returned by Apply when the patch succeeds but produces no
// rollback patch (the operation is its own inverse or is irreversible).
var errNoRollback = errors.New("live DAG executor: no rollback patch")

func newLiveDAGPatchExecutor(mgr *ares_runtime.Manager, agentID string) *liveDAGPatchExecutor {
	return &liveDAGPatchExecutor{mgr: mgr, agentID: agentID}
}

func (e *liveDAGPatchExecutor) Name() string { return "live_dag" }

func (e *liveDAGPatchExecutor) Snapshot(_ context.Context) (any, error) {
	return nil, errNoSnapshot
}

func (e *liveDAGPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	// All patch types that GraphPatchExecutor supports are supported here.
	switch p.Type {
	case patch.PatchInsertNode, patch.PatchRemoveNode,
		patch.PatchReplaceNode, patch.PatchAddEdge,
		patch.PatchRemoveEdge, patch.PatchChangeScheduler:
		return nil
	default:
		return fmt.Errorf("live DAG executor: unsupported patch type %s", p.Type)
	}
}

func (e *liveDAGPatchExecutor) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	dagAny, ok := e.mgr.GetAgentDAG(e.agentID)
	if !ok || dagAny == nil {
		return nil, fmt.Errorf("live DAG executor: no DAG for agent %s", e.agentID)
	}
	dag, dagOk := dagAny.(*engine.MutableDAG)
	if !dagOk || dag == nil {
		return nil, fmt.Errorf("live DAG executor: DAG for agent %s is not a MutableDAG", e.agentID)
	}

	switch p.Type {
	case patch.PatchInsertNode:
		step := &engine.Step{ID: p.Target, Name: p.Target, AgentType: "processor"}
		if err := dag.AddNode(ctx, step); err != nil {
			return nil, fmt.Errorf("live DAG: insert node %s: %w", p.Target, err)
		}
		return &patch.RuntimePatch{
			Type:   patch.PatchRemoveNode,
			Target: p.Target,
			Reason: "rollback: remove inserted node",
		}, nil

	case patch.PatchRemoveNode:
		if err := dag.RemoveNode(ctx, p.Target); err != nil {
			return nil, fmt.Errorf("live DAG: remove node %s: %w", p.Target, err)
		}
		return nil, errNoRollback

	case patch.PatchReplaceNode:
		step := &engine.Step{ID: p.Target, Name: p.Target, AgentType: "processor"}
		if err := dag.RemoveNode(ctx, p.Target); err != nil {
			return nil, fmt.Errorf("live DAG: replace (remove) node %s: %w", p.Target, err)
		}
		if err := dag.AddNode(ctx, step); err != nil {
			return nil, fmt.Errorf("live DAG: replace (add) node %s: %w", p.Target, err)
		}
		return nil, errNoRollback

	case patch.PatchAddEdge:
		val, ok := p.Value.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("live DAG: AddEdge value must be map[string]string")
		}
		from, to := val["from"], val["to"]
		if err := dag.AddEdge(ctx, from, to); err != nil {
			return nil, fmt.Errorf("live DAG: add edge %s→%s: %w", from, to, err)
		}
		return &patch.RuntimePatch{
			Type:   patch.PatchRemoveEdge,
			Value:  map[string]string{"from": from, "to": to},
			Reason: "rollback: remove added edge",
		}, nil

	case patch.PatchRemoveEdge:
		val, ok := p.Value.(map[string]string)
		if !ok {
			return nil, fmt.Errorf("live DAG: RemoveEdge value must be map[string]string")
		}
		from, to := val["from"], val["to"]
		if err := dag.RemoveEdge(ctx, from, to); err != nil {
			return nil, fmt.Errorf("live DAG: remove edge %s→%s: %w", from, to, err)
		}
		return nil, errNoRollback

	case patch.PatchChangeScheduler:
		// Store the scheduler type on the live DAG so the agent's runtime
		// scheduler selection reads the evolved config instead of the default.
		schedType := fmt.Sprintf("%T", p.Value)
		dag.SchedulerType = schedType
		log.Printf("live DAG: scheduler change for agent %s: %s", e.agentID, schedType)
		return nil, errNoRollback

	default:
		return nil, fmt.Errorf("live DAG executor: unsupported patch type %s", p.Type)
	}
}

// Ensure liveDAGPatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*liveDAGPatchExecutor)(nil)
