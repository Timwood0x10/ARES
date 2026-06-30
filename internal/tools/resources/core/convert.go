package core

import (
	llmcore "github.com/Timwood0x10/ares/api/core"
)

// ToolSchemaToLLMTool converts a ToolSchema from the registry to api/core.Tool
// for passing to the LLM Chat API.
func ToolSchemaToLLMTool(schema ToolSchema) llmcore.Tool {
	return llmcore.Tool{
		Type: "function",
		Function: llmcore.FunctionDefinition{
			Name:        schema.Name,
			Description: schema.Description,
			Parameters:  ParameterSchemaToMap(schema.Parameters),
		},
	}
}

// ParameterSchemaToMap converts *ParameterSchema to map[string]interface{}
// for the JSON Schema format expected by api/core.FunctionDefinition.Parameters.
// Returns nil if the schema is nil.
func ParameterSchemaToMap(schema *ParameterSchema) map[string]interface{} {
	if schema == nil {
		return nil
	}
	result := map[string]interface{}{
		"type": schema.Type,
	}
	if len(schema.Properties) > 0 {
		props := make(map[string]interface{}, len(schema.Properties))
		for name, p := range schema.Properties {
			prop := map[string]interface{}{
				"type": p.Type,
			}
			if p.Description != "" {
				prop["description"] = p.Description
			}
			if p.Default != nil {
				prop["default"] = p.Default
			}
			if len(p.Enum) > 0 {
				prop["enum"] = p.Enum
			}
			if p.Min != nil {
				prop["minimum"] = *p.Min
			}
			if p.Max != nil {
				prop["maximum"] = *p.Max
			}
			props[name] = prop
		}
		result["properties"] = props
	}
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}
	return result
}
