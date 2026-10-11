// Package planprojection projects workflow engine steps into taskfabric PlanSteps.
//
// This is the SINGLE projection function that maps engine.Step →
// taskfabric.PlanStep, closing the "two graphs" gap in the evolution loop:
// before this package, the live MutableDAG and the compiled task set were
// separate objects that could silently diverge. The projection lives in
// its own package (not in taskfabric) so the kernel never imports the
// planner package — the caller (cmd layer) projects engine.Step onto
// PlanStep, then hands the batch to Fabric.CompilePlan.
package planprojection

import (
	"strconv"
	"time"

	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// CompileRecord captures the provenance of one CompilePlan call so the
// introspect layer can answer "which generation, which gate, which compile
// produced the current task set".
type CompileRecord struct {
	Generation int      `json:"generation"`
	DAGVersion uint64   `json:"dag_version"`
	CompileID  string   `json:"compile_id"`
	PlanIDs    []string `json:"plan_ids"`
	StepCount  int      `json:"step_count"`
}

// ProjectStep converts a single engine.Step into a taskfabric.PlanStep.
//
// Mapping rules (fixed):
//
//	PlanStep.ID          ← Step.ID
//	PlanStep.Capability  ← Step.AgentType
//	PlanStep.DependsOn   ← Step.DependsOn (copied)
//	PlanStep.MaxRetries  ← Step.RetryPolicy.MaxAttempts (nil → 0)
//	PlanStep.BackoffBase ← Step.RetryPolicy.InitialDelay (nil → 0)
//	PlanStep.BackoffMax  ← Step.RetryPolicy.MaxDelay (nil → 0)
//	PlanStep.Deadline    ← Step.Timeout (relative budget; CompilePlan resolves
//	                       it to the absolute Task.Deadline)
//	PlanStep.AllowPartial ← Step.AllowPartial
//	PlanStep.Priority    ← Step.Metadata["priority"] parsed as int; parse
//	                       failure or missing → 0
//	PlanStep.Payload     ← {"input": Step.Input} merged with Step.Metadata
//	                       (metadata keys win on conflict)
//	PlanStep.Origin      ← not filled (json:"-", stamped by the Kernel)
//
// RetryPolicy.BackoffMultiplier is NOT forwarded: the fabric backoff always
// doubles (BackoffBase×2^n capped by BackoffMax). A non-2 multiplier in the
// engine step is therefore not honoured at execution — see ADR-C1 in
// plan/0.3.3_task.md for why the two retry models are mapped, not merged.
//
// Explicitly discarded (HITL frozen or execution-time state):
//   - Interrupt, RecoveryPolicy, Name,
//     Status/Output/Error/StartedAt/FinishedAt
func ProjectStep(s *engine.Step) taskfabric.PlanStep {
	if s == nil {
		return taskfabric.PlanStep{}
	}

	deps := make([]string, len(s.DependsOn))
	copy(deps, s.DependsOn)

	maxRetries := 0
	var backoffBase, backoffMax time.Duration
	if s.RetryPolicy != nil {
		maxRetries = s.RetryPolicy.MaxAttempts
		backoffBase = s.RetryPolicy.InitialDelay
		backoffMax = s.RetryPolicy.MaxDelay
	}

	payload := map[string]any{
		"input": s.Input,
	}
	for k, v := range s.Metadata {
		payload[k] = v
	}

	return taskfabric.PlanStep{
		ID:           s.ID,
		Capability:   s.AgentType,
		DependsOn:    deps,
		MaxRetries:   maxRetries,
		BackoffBase:  backoffBase,
		BackoffMax:   backoffMax,
		Deadline:     s.Timeout,
		AllowPartial: s.AllowPartial,
		Priority:     parsePriority(s.Metadata),
		Payload:      payload,
		SessionID:    parseSessionID(s.Metadata),
		TenantID:     parseTenantID(s.Metadata),
	}
}

// ProjectSteps converts a batch of engine.Steps into PlanSteps. The order
// is preserved (callers rely on input order for deterministic CompilePlan).
func ProjectSteps(steps []*engine.Step) []taskfabric.PlanStep {
	result := make([]taskfabric.PlanStep, 0, len(steps))
	for _, s := range steps {
		result = append(result, ProjectStep(s))
	}
	return result
}

// parsePriority reads the "priority" key from step metadata. A missing or
// unparseable value yields 0 (normal priority). We try int first, then
// string-to-int, matching the loose typing of map[string]string metadata.
func parsePriority(meta map[string]string) int {
	if meta == nil {
		return 0
	}
	raw, ok := meta["priority"]
	if !ok || raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}

// parseSessionID reads the "session_id" key from step metadata. A missing or
// empty value yields "" (session-less, legacy behavior). This is how the L2
// session root's SessionID propagates to every tool/plan node grown from it:
// the plannerCognition stamps session_id into the node's Metadata, and
// ProjectStep carries it into the PlanStep so CompilePlan can stamp it onto
// the checkpoint envelope.
func parseSessionID(meta map[string]string) string {
	if meta == nil {
		return ""
	}
	return meta["session_id"]
}

// parseTenantID reads the "tenant_id" key from step metadata — the same
// unprefixed-metadata contract session_id uses. The planner stamps it onto
// grown nodes from the executing task's tenant so a session's grown tool and
// plan tasks execute (and recall knowledge) under the session's tenant
// instead of the process default. A missing or non-string value yields "".
func parseTenantID(meta map[string]string) string {
	if meta == nil {
		return ""
	}
	return meta["tenant_id"]
}
