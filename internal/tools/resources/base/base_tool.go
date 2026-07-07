package base

import (
	"context"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// Compile-time check: BaseTool satisfies ToolLifecycle.
var _ core.ToolLifecycle = (*BaseTool)(nil)

// BaseTool provides common tool functionality.
type BaseTool struct {
	name         string
	description  string
	category     core.ToolCategory
	capabilities []core.Capability
	parameters   *core.ParameterSchema
	metadata     *core.ToolMetadata
	tags         map[string]string
}

// Tags returns the tool's semantic metadata tags for LLM routing.
func (t *BaseTool) Tags() map[string]string {
	if t.tags == nil {
		return map[string]string{}
	}
	cp := make(map[string]string, len(t.tags))
	for k, v := range t.tags {
		cp[k] = v
	}
	return cp
}

// SetTags sets the tool's semantic metadata tags.
func (t *BaseTool) SetTags(tags map[string]string) {
	t.tags = tags
}

// Init is a no-op lifecycle hook. Override in embedding types as needed.
func (t *BaseTool) Init(ctx context.Context) error {
	return nil
}

// Stop is a no-op lifecycle hook. Override in embedding types as needed.
func (t *BaseTool) Stop(ctx context.Context) error {
	return nil
}

// NewBaseTool creates a new BaseTool.
func NewBaseTool(name, description string, params *core.ParameterSchema) *BaseTool {
	return &BaseTool{
		name:         name,
		description:  description,
		category:     core.CategoryCore, // Default category
		capabilities: []core.Capability{},
		parameters:   params,
		metadata:     nil,
	}
}

// NewBaseToolWithCategory creates a new BaseTool with a specific category.
func NewBaseToolWithCategory(name, description string, category core.ToolCategory, params *core.ParameterSchema) *BaseTool {
	return &BaseTool{
		name:         name,
		description:  description,
		category:     category,
		capabilities: []core.Capability{},
		parameters:   params,
		metadata:     nil,
	}
}

// NewBaseToolWithCapabilities creates a new BaseTool with specific capabilities.
func NewBaseToolWithCapabilities(name, description string, category core.ToolCategory, capabilities []core.Capability, params *core.ParameterSchema) *BaseTool {
	return &BaseTool{
		name:         name,
		description:  description,
		category:     category,
		capabilities: capabilities,
		parameters:   params,
		metadata:     nil,
	}
}

// Name returns the tool name.
func (t *BaseTool) Name() string {
	return t.name
}

// Description returns the tool description.
func (t *BaseTool) Description() string {
	return t.description
}

// Category returns the tool category.
func (t *BaseTool) Category() core.ToolCategory {
	return t.category
}

// Capabilities returns the tool capabilities.
func (t *BaseTool) Capabilities() []core.Capability {
	return t.capabilities
}

// Parameters returns the parameter schema.
func (t *BaseTool) Parameters() *core.ParameterSchema {
	return t.parameters
}

// Metadata returns the tool metadata.
func (t *BaseTool) Metadata() *core.ToolMetadata {
	return t.metadata
}

// ToolFunc is a function-based tool.
type ToolFunc struct {
	BaseTool
	fn func(ctx context.Context, params map[string]interface{}) (core.Result, error)
}

// NewToolFunc creates a new ToolFunc.
func NewToolFunc(
	name, description string,
	params *core.ParameterSchema,
	fn func(ctx context.Context, params map[string]interface{}) (core.Result, error),
) *ToolFunc {
	return &ToolFunc{
		BaseTool: *NewBaseTool(name, description, params),
		fn:       fn,
	}
}

// Execute executes the tool function.
func (t *ToolFunc) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	return t.fn(ctx, params)
}

// WithMetadata adds metadata to a tool.
func WithMetadata(tool core.Tool, metadata core.ToolMetadata) core.Tool {
	return &metadataTool{
		Tool:     tool,
		Metadata: metadata,
	}
}

// metadataTool wraps a tool with metadata.
type metadataTool struct {
	core.Tool
	Metadata core.ToolMetadata
}

// WithToolTags adds semantic tags to a tool for LLM-based routing.
// Tags are key-value pairs describing the tool's domain, input/output types,
// side effects, and other metadata. Standard tag keys:
//
//	domain: math, text, network, file, system, knowledge, memory, data, pdf
//	input_type: text, json, number, file, url, array
//	output_type: text, json, number, boolean, file
//	side_effects: true, false (does the tool change external state)
//	requires_network: true, false
//	mutates_state: true, false (does the tool change internal app state)
//
// Usage:
//
//	tool := base.WithToolTags(
//	    builtin_math.NewCalculator(),
//	    map[string]string{"domain": "math", "input_type": "text", "output_type": "number"},
//	)
func WithToolTags(tool core.Tool, tags map[string]string) core.Tool {
	return &taggedTool{
		Tool: tool,
		tags: tags,
	}
}

// taggedTool wraps a tool with semantic tags.
type taggedTool struct {
	core.Tool
	tags map[string]string
}

// Tags returns the tool's semantic metadata tags.
func (t *taggedTool) Tags() map[string]string {
	if t.tags == nil {
		return map[string]string{}
	}
	cp := make(map[string]string, len(t.tags))
	for k, v := range t.tags {
		cp[k] = v
	}
	return cp
}

// Compile-time check: taggedTool satisfies core.TaggableTool.
var _ core.TaggableTool = (*taggedTool)(nil)
