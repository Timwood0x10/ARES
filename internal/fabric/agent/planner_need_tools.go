package agentfabric

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llmcore"
)

// NeedToolsToolName is the tool a quantum calls to ask for capabilities the current
// advertisement does not cover.
//
// It exists because narrowing is derived from a keyword table, and a keyword table is wrong in
// the partial direction far more often than the total one: a task that needs network AND git
// but only contains network keywords narrows successfully (no fail-open, no warning) and hides
// the git tool completely. Without this hatch the only recovery is a retry that fails the same
// way, so the model is stuck with a toolbelt it cannot describe as insufficient.
const NeedToolsToolName = "need_tools"

// needToolsArgKey is the argument carrying the requested capability names.
const needToolsArgKey = "extra_caps"

// Schema literals shared by every injected tool definition (rationale, need_tools, context):
// goconst counts them per package, and one spelling is one place to change if the wire format
// ever moves.
const (
	toolTypeFunction    = "function"
	schemaKeyType       = "type"
	schemaKeyProperties = "properties"
	schemaKeyRequired   = "required"
)

// needToolsToolSchema advertises the hatch. Like the rationale tool it is injected into the
// prompt only and is not in the registry: it performs no work, it changes what the next quantum
// is offered.
func needToolsToolSchema() llmcore.Tool {
	return llmcore.Tool{
		Type: toolTypeFunction,
		Function: llmcore.FunctionDefinition{
			Name: NeedToolsToolName,
			Description: "Ask for tool capabilities that are missing from the tools you were given. " +
				"Use it when the task clearly needs something you cannot see (for example a git " +
				"or HTTP tool) instead of guessing with a tool that cannot do the job. The " +
				"requested capabilities are added to the next step's tools; this call itself " +
				"performs no work.",
			Parameters: map[string]interface{}{
				schemaKeyType: "object",
				schemaKeyProperties: map[string]interface{}{
					needToolsArgKey: map[string]interface{}{
						schemaKeyType: "array",
						"items":       map[string]interface{}{schemaKeyType: "string"},
						"description": "Capability names to add, e.g. [\"network\", \"file\"].",
					},
				},
				schemaKeyRequired: []string{needToolsArgKey},
			},
		},
	}
}

// applyNeedTools unions the requested capabilities into the task payload so the NEXT quantum
// advertises them. The task is the right place: the capability set is per task, while the
// planner serving it is shared by every session.
func (c *plannerCognition) applyNeedTools(_ context.Context, task *models.Task, call llmcore.ToolCall) {
	if task == nil {
		return
	}
	extra := requestedCapabilities(call)
	if len(extra) == 0 {
		return
	}
	if task.Payload == nil {
		task.Payload = make(map[string]any, 1)
	}
	// Read through the same parser the router uses rather than asserting []string here: the
	// payload may hold []string, []any or a comma-separated string (SDK/CLI submitters write
	// those), and a hard assertion would silently drop what was already there, leaving only the
	// caps requested now. Read and write must share one parser or they drift.
	merged := unionCapabilities(explicitTaskCapabilities(task), extra)
	task.Payload[TaskToolCapabilityKey] = merged
	if c.logger != nil {
		c.logger.Info("planner: capabilities widened by request",
			"requested", extra, "now", merged, "task_id", task.TaskID)
	}
}

// requestedCapabilities parses the hatch's argument. Malformed JSON yields nothing rather than
// failing the quantum: the call is a hint, and an unparseable hint must not end the task.
func requestedCapabilities(call llmcore.ToolCall) []string {
	if call.Function.Arguments == "" {
		return nil
	}
	var args struct {
		Caps []string `json:"extra_caps"`
	}
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return nil
	}
	out := make([]string, 0, len(args.Caps))
	for _, cap := range args.Caps {
		if cap != "" {
			out = append(out, cap)
		}
	}
	return out
}

// unionCapabilities merges two capability sets without duplicates, sorted for a stable prompt.
func unionCapabilities(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, list := range [][]string{a, b} {
		for _, cap := range list {
			if cap == "" || seen[cap] {
				continue
			}
			seen[cap] = true
			out = append(out, cap)
		}
	}
	sort.Strings(out)
	return out
}
