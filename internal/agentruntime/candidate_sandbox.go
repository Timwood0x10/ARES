package agentruntime

import (
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/agents"
)

// EvaluationToolCeiling is the tool whitelist an evaluation runtime runs with: the set a
// candidate may NOT exceed.
//
// It is derived from declarations that already exist rather than invented here: every
// builtin tool is registered with `side_effects` / `mutates_state` tags
// (internal/tools/resources/builtin.go), and this list is the read-only subset of them,
// minus the tools whose RESULT is not comparable across runs:
//
//   - excluded by declaration: FileTools (writes files), CodeRunner (arbitrary code),
//     Knowledge{Add,Update,Delete,Correct} (mutate the knowledge store), the mutating
//     HTTPRequest registration;
//   - excluded by judgement: WebSearch / HTTPRequest / EmbeddingTool (they reach outside
//     the process: results change between runs, they cost money and they rate-limit —
//     an A/B whose two sides saw different search results is measuring the web, not the
//     candidate) and PDFTool (its input is a file that must happen to exist on the host).
//
// Adding one back is a one-line change here — deliberately, because "should candidates
// be graded with live web access" is a measurement question, not a safety one, and it
// should be answered by whoever owns the measurement.
var EvaluationToolCeiling = []string{
	// Pure computation / text: no state, no network, same answer every time.
	"calculator",
	"datetime",
	"text_processor",
	"json_tools",
	"data_validation",
	"data_transform",
	"regex_tool",
	"log_analyzer",
	"id_generator",
	"hash_tool",
	"string_utils",
	// Read-only against production data: the candidate sees the same corpus the server
	// would, which is what makes a case realistic, and it cannot change any of it.
	"knowledge_search",
	"memory_search",
	"user_profile",
	"task_planner",
}

// SandboxCandidateTools returns a COPY of the candidate whose tool whitelist is narrowed
// to the ceiling, or an error when that would leave it with no tool at all.
//
// The copy matters: the strategy handed in is usually the one the evolution path would
// go on to promote, so installing evaluation-only restrictions on it in place would leak
// them into production. Params is rebuilt rather than edited for the same reason (a
// shared map read by many concurrent tasks must not be written to).
//
// The ceiling is a MAXIMUM, never a suggestion: a candidate that asks for more gets the
// ceiling, and one that asks for less keeps less. The reverse (the candidate's own list
// wins, or widens the ceiling) would let the thing under evaluation decide how much of
// the environment it may touch.
//
// The error case is not decoration. The whitelist encoding cannot express "no tools":
// an empty or missing value means "NO FILTER — advertise everything" (see
// agents.ParamKeyTools). So a candidate whose entire list falls outside the ceiling must
// be REFUSED here rather than quietly sandboxed to nothing — the quiet version would
// hand it every registered tool, which is the widest boundary in the system, reached by
// asking for the narrowest one.
func SandboxCandidateTools(candidate *agents.ActiveStrategy, ceiling []string) (*agents.ActiveStrategy, error) {
	if candidate == nil {
		return nil, fmt.Errorf("agentruntime: sandbox needs a candidate strategy")
	}
	allowed := make(map[string]bool, len(ceiling))
	for _, name := range ceiling {
		allowed[strings.TrimSpace(name)] = true
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("agentruntime: evaluation tool ceiling is empty — every candidate would be refused")
	}

	requested := agents.ToolNamesFromParams(candidate.Params)
	effective := requested
	if len(requested) == 0 {
		// No whitelist of its own: the ceiling is what it gets.
		effective = ceiling
	}

	kept := make([]string, 0, len(effective))
	var refused []string
	for _, name := range effective {
		if allowed[name] {
			kept = append(kept, name)
			continue
		}
		refused = append(refused, name)
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf(
			"agentruntime: candidate %q requests only tools outside the evaluation ceiling (%s) — refusing to run it, "+
				"because an empty whitelist means \"no filter\" and would grant every tool instead of none",
			candidate.ID, strings.Join(refused, ", "))
	}

	params := make(map[string]any, len(candidate.Params)+1)
	for k, v := range candidate.Params {
		params[k] = v
	}
	params[agents.ParamKeyTools] = strings.Join(kept, ",")

	sandboxed := *candidate
	sandboxed.Params = params
	return &sandboxed, nil
}
