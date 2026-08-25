package envcap

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// SearchToolName is the registered name of the environment-capability search
// tool. Agents call it to discover what tools, skills, and native commands the
// running environment offers before attempting to use them (progressive
// disclosure: this returns name + one-line description; full details load on
// demand when the capability is actually invoked).
const SearchToolName = "search_capabilities"

// defaultSearchLimit caps results when the caller does not specify a limit, so
// a broad query cannot flood the LLM context with the entire environment.
const defaultSearchLimit = 20

// SearchTool adapts a *Searcher into a core.Tool so the environment-capability
// search is reachable from the agent's tool-selection path. This completes the
// SKILLS progressive-disclosure story: the memory manager surfaces a resident
// skill block, and this tool lets the agent actively search across tools,
// skills, and native commands by keyword.
type SearchTool struct {
	*base.BaseTool
	searcher *Searcher
}

// NewSearchTool wraps a Searcher as a core.Tool. The searcher must be non-nil.
func NewSearchTool(searcher *Searcher) *SearchTool {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"query": {
				Type:        "string",
				Description: "Keyword to match against capability names and descriptions (case-insensitive substring). Example: \"git\", \"search\", \"file\".",
			},
			"limit": {
				Type:        "integer",
				Description: "Maximum number of results to return. Omit or set <= 0 for the default cap.",
			},
		},
		Required: []string{"query"},
	}

	return &SearchTool{
		BaseTool: base.NewBaseToolWithCapabilities(
			SearchToolName,
			"Search the running environment for available capabilities (registered tools, skills, and allowlisted native commands) matching a keyword. Returns name, kind, and a one-line description; call the returned capability directly to use it.",
			core.CategoryKnowledge,
			[]core.Capability{core.CapabilityKnowledge},
			params,
		),
		searcher: searcher,
	}
}

// Execute runs the capability search. It never returns a transport error: a bad
// query or empty result is reported through the Result payload so the agent can
// reason about it (consistent with the other builtin tools).
func (t *SearchTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return core.NewErrorResult("query is required"), nil
	}

	limit := defaultSearchLimit
	if raw, present := params["limit"]; present {
		if n, convOK := toInt(raw); convOK && n > 0 {
			limit = n
		}
	}

	caps, err := t.searcher.Search(ctx, query, limit)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("capability search failed: %v", err)), nil
	}

	items := make([]map[string]interface{}, 0, len(caps))
	for _, c := range caps {
		items = append(items, map[string]interface{}{
			"kind":        string(c.Kind),
			"name":        c.Name,
			"description": c.Description,
		})
	}
	return core.NewResult(true, map[string]interface{}{
		"query":   query,
		"count":   len(items),
		"results": items,
	}), nil
}

// toInt coerces the JSON-decoded numeric forms (float64 from JSON, or native
// int) into an int. Returns false when the value is not numeric.
func toInt(v interface{}) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}
