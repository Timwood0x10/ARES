package toolsource

import (
	"context"
	"encoding/json"
	"strings"

	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// DiscoverToolsName is the well-known name of the runtime discovery meta-tool.
// The same constant is duplicated in package agentloop to keep that package
// dependency-free; a shared test asserts both constants are equal.
const DiscoverToolsName = "discover_tools"

// maxDiscoverResults caps the number of tools returned to keep the LLM
// context small. The Engine expands chosen names into full tool defs via
// ToolExpander.
const maxDiscoverResults = 20

// queryParam is the JSON Schema parameter name of the discover_tools
// meta-tool. Repeated across Parameters/Required/Execute lookup; extracted to
// a constant to stay goconst-clean.
const queryParam = "query"

// discoverToolEntry is the compact JSON object returned per match: name +
// description only (full schemas are expanded later by ToolExpander).
type discoverToolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// discoverToolsTool implements core.Tool to let the LLM search the ToolSource
// at runtime by name, description, or tag.
type discoverToolsTool struct {
	source ToolSource
}

// NewDiscoverToolsTool builds the discover_tools meta-tool bound to source.
// If source is nil, Execute returns ErrNilToolSource.
func NewDiscoverToolsTool(source ToolSource) core.Tool {
	return &discoverToolsTool{source: source}
}

// Name returns the well-known meta-tool name.
func (t *discoverToolsTool) Name() string { return DiscoverToolsName }

// Description returns the LLM-visible description.
func (t *discoverToolsTool) Description() string {
	return "Search available tools by name, description, or tag. " +
		"Returns a JSON array of {name, description} entries; matched tools become " +
		"callable in your next turn. Pick the one you need and call it by name."
}

// Category returns CategorySystem (this is a meta-tool, not a domain capability).
func (t *discoverToolsTool) Category() core.ToolCategory { return core.CategorySystem }

// Capabilities returns none: this is a meta-tool, not a domain capability.
func (t *discoverToolsTool) Capabilities() []core.Capability { return nil }

// Parameters returns a single required "query" string parameter.
func (t *discoverToolsTool) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			queryParam: {
				Type:        "string",
				Description: "Search query: matches tool name, description, or tags (case-insensitive substring).",
			},
		},
		Required: []string{queryParam},
	}
}

// Execute searches the source's tools and returns matching {name, description}
// entries as Result.Data. Data is a JSON-encoded string (a JSON array of
// objects) so the agentloop engine can parse it with encoding/json after the
// runtime formats Result.Data with %v, and so the LLM sees valid JSON in the
// tool message. Up to maxDiscoverResults matches are returned.
func (t *discoverToolsTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	if t == nil || t.source == nil {
		return core.Result{}, ErrNilToolSource
	}
	query, _ := params[queryParam].(string)
	if strings.TrimSpace(query) == "" {
		return core.NewErrorResult(queryParam + " is required"), nil
	}
	tools, err := t.source.Tools(ctx)
	if err != nil {
		return core.NewErrorResult(err.Error()), nil
	}
	entries := searchTools(tools, query)
	// Marshal to a JSON string so %v formatting yields valid JSON for both the
	// LLM (tool message) and the engine's expandDiscoveredTools parser.
	b, err := json.Marshal(entries)
	if err != nil {
		return core.NewErrorResult("marshal discover results: " + err.Error()), nil
	}
	return core.NewResult(true, string(b)), nil
}

// searchTools returns up to maxDiscoverResults compact entries whose name,
// description, or tags match query (case-insensitive substring).
func searchTools(tools []core.Tool, query string) []discoverToolEntry {
	q := strings.ToLower(query)
	entries := make([]discoverToolEntry, 0, maxDiscoverResults)
	for _, t := range tools {
		if t == nil {
			continue
		}
		if !toolMatches(t, q) {
			continue
		}
		entries = append(entries, discoverToolEntry{
			Name:        t.Name(),
			Description: t.Description(),
		})
		if len(entries) >= maxDiscoverResults {
			break
		}
	}
	return entries
}

// toolMatches reports whether tool matches query q (already lowercased) by
// name, description, or any tag key/value substring.
func toolMatches(t core.Tool, q string) bool {
	if strings.Contains(strings.ToLower(t.Name()), q) {
		return true
	}
	if strings.Contains(strings.ToLower(t.Description()), q) {
		return true
	}
	if tt, ok := t.(core.TaggableTool); ok {
		for k, v := range tt.Tags() {
			if strings.Contains(strings.ToLower(k), q) || strings.Contains(strings.ToLower(v), q) {
				return true
			}
		}
	}
	return false
}
