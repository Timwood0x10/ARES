package core

import (
	"context"
	"strings"
)

// ToolCategory represents the category of a tool.
type ToolCategory string

const (
	// CategorySystem represents system-level tools (file operations, ID generation, etc.)
	CategorySystem ToolCategory = "system"
	// CategoryCore represents core general-purpose tools (HTTP, calculator, datetime, etc.)
	CategoryCore ToolCategory = "core"
	// CategoryData represents data processing tools (JSON, validation, etc.)
	CategoryData ToolCategory = "data"
	// CategoryKnowledge represents knowledge base tools
	CategoryKnowledge ToolCategory = "knowledge"
	// CategoryMemory represents memory-related tools
	CategoryMemory ToolCategory = "memory"
	// CategoryExternal represents external MCP tools connected via MCP protocol.
	CategoryExternal ToolCategory = "external"
)

// Parameter defines a single parameter.
type Parameter struct {
	Type        string        `json:"type"`
	Description string        `json:"description"`
	Default     interface{}   `json:"default,omitempty"`
	Enum        []interface{} `json:"enum,omitempty"`
	Min         *float64      `json:"min,omitempty"`
	Max         *float64      `json:"max,omitempty"`
}

// ParameterSchema defines tool parameters.
type ParameterSchema struct {
	Type       string                `json:"type"`
	Properties map[string]*Parameter `json:"properties"`
	Required   []string              `json:"required"`
}

// GetType returns the parameter type.
func (p *ParameterSchema) GetType() string {
	if p == nil {
		return ""
	}
	return p.Type
}

// GetProperties returns the parameter properties.
func (p *ParameterSchema) GetProperties() map[string]*Parameter {
	if p == nil {
		return nil
	}
	return p.Properties
}

// GetRequired returns the required parameters.
func (p *ParameterSchema) GetRequired() []string {
	if p == nil {
		return nil
	}
	return p.Required
}

// ToolMetadata holds additional tool metadata.
type ToolMetadata struct {
	Version     string
	Author      string
	Tags        []string
	Examples    []string
	Deprecated  bool
	Deprecation string
}

// Tool represents an executable tool.
type Tool interface {
	// Name returns the tool name.
	Name() string
	// Description returns the tool description.
	Description() string
	// Category returns the tool category.
	Category() ToolCategory
	// Capabilities returns the capabilities this tool provides.
	Capabilities() []Capability
	// Execute executes the tool with given parameters.
	Execute(ctx context.Context, params map[string]interface{}) (Result, error)
	// Parameters returns the parameter schema.
	Parameters() *ParameterSchema
}

// IdempotentTool is an optional interface that tools can implement to
// indicate they are safe to retry on partial failure. Pure computation
// tools (calculators, text processors, validators, etc.) should return
// true. Tools with side effects (file I/O, network calls, state
// mutations) should not implement this interface or return false.
type IdempotentTool interface {
	IsIdempotent() bool
}

// TaggableTool is an optional interface for tools that provide semantic tags
// for LLM-based routing and discovery.
//
// Tags are key-value string pairs that describe the tool's domain, input/output
// types, side effects, and other metadata. The LLM uses these tags to determine
// which tool to call for a given task.
//
// Standard tag keys:
//
//	domain: math, text, network, file, system, knowledge, memory, data, pdf
//	input_type: text, json, number, file, url, array
//	output_type: text, json, number, boolean, file
//	side_effects: true, false
//	requires_network: true, false
//	mutates_state: true, false
//	idempotent: true, false   (declares that calling the tool twice is safe; see DeclaresIdempotent)
type TaggableTool interface {
	// Tags returns semantic metadata tags for LLM routing.
	Tags() map[string]string
}

// TagIdempotent is the schema tag a tool uses to declare that calling it twice is safe.
//
// It lives here, next to ToolSchema, so the producer (a tool's own tag map) and the consumer (the
// retry classifier) cannot drift apart on the spelling. Absent or unparseable means "not
// declared", which the retry path treats as unsafe — the same default as BindTool.
const TagIdempotent = "idempotent"

// DeclaresIdempotent reports whether a tag map says the tool is safe to call repeatedly.
//
// The accepted spellings are the ones a YAML/JSON author is likely to write. Anything else is
// treated as "not declared" rather than as an error: a typo in a tag must not start retrying an
// unmarked write.
func DeclaresIdempotent(tags map[string]string) bool {
	if v, ok := parseDeclaredBool(tags[TagIdempotent]); ok && v {
		return true
	}
	// "Changes no state" answers the same question: a retry has nothing to duplicate. Recognising
	// the existing vocabulary here avoids a second, parallel set of tags for authors to learn.
	// Tri-state, not "not true means false": a value the parser does not recognise is NOT a
	// declaration, and treating it as one would turn a typo ("side_effects: flase") into permission
	// to retry an unmarked write.
	if v, ok := parseDeclaredBool(tags["side_effects"]); ok {
		return !v
	}
	if v, ok := parseDeclaredBool(tags["mutates_state"]); ok {
		return !v
	}
	return false
}

// parseDeclaredBool accepts the spellings a YAML/JSON author is likely to write and reports
// whether the value was one of them. The ok result is what separates "declared false" from "not
// declared": collapsing the two would make a typo mean "no side effects", which is exactly the
// reading that permits a duplicate write.
func parseDeclaredBool(v string) (value, ok bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "y":
		return true, true
	case "false", "0", "no", "n":
		return false, true
	default:
		return false, false
	}
}

// ToolSchema represents the schema of a tool for capability export.
type ToolSchema struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Category    ToolCategory      `json:"category"`
	Parameters  *ParameterSchema  `json:"parameters"`
	Tags        map[string]string `json:"tags,omitempty"`
}
