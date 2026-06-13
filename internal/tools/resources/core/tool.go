package core

import "context"

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
	// CategoryDomain represents domain-specific tools (weather, search, etc.)
	CategoryDomain ToolCategory = "domain"
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

// ToolSchema represents the schema of a tool for capability export.
type ToolSchema struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Category    ToolCategory     `json:"category"`
	Parameters  *ParameterSchema `json:"parameters"`
}
