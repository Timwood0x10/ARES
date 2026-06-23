package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// JSONTools provides JSON processing operations.
type JSONTools struct {
	*base.BaseTool
}

// NewJSONTools creates a new JSONTools tool.
func NewJSONTools() *JSONTools {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (parse, extract, merge, pretty)",
				Enum:        []interface{}{"parse", "extract", "merge", "pretty"},
			},
			"data": {
				Type:        "string",
				Description: "JSON string to process",
			},
			"path": {
				Type:        "string",
				Description: "JSONPath or key to extract (required for extract operation)",
			},
			"merge_data": {
				Type:        "string",
				Description: "JSON string to merge (required for merge operation)",
			},
			"indent": {
				Type:        "string",
				Description: "Indentation string for pretty printing (default: '  ')",
			},
		},
		Required: []string{"operation", "data"},
	}

	return &JSONTools{
		BaseTool: base.NewBaseToolWithCapabilities("json_tools", "Parse, extract, merge, and pretty-print JSON", core.CategoryData, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the JSON operation.
func (t *JSONTools) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	data, ok := params["data"].(string)
	if !ok || data == "" {
		return core.NewErrorResult("data is required"), nil
	}

	switch operation {
	case "parse":
		return t.parse(ctx, data)
	case "extract":
		path, ok := params["path"].(string)
		if !ok || path == "" {
			return core.NewErrorResult("path is required for extract operation"), nil
		}
		return t.extract(ctx, data, path)
	case "merge":
		mergeData, ok := params["merge_data"].(string)
		if !ok || mergeData == "" {
			return core.NewErrorResult("merge_data is required for merge operation"), nil
		}
		return t.merge(ctx, data, mergeData)
	case "pretty":
		indent := getString(params, "indent")
		if indent == "" {
			indent = "  "
		}
		return t.pretty(ctx, data, indent)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// parse parses a JSON string and returns its structure.
func (t *JSONTools) parse(ctx context.Context, data string) (core.Result, error) {
	var js interface{}
	if err := json.Unmarshal([]byte(data), &js); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation": "parse",
		"valid":     true,
		"parsed":    js,
	}), nil
}

// extract extracts a value from JSON using a simple path notation.
// Supports dot notation (e.g., "user.name") and array indices (e.g., "items[0]").
func (t *JSONTools) extract(ctx context.Context, data, path string) (core.Result, error) {
	var js interface{}
	if err := json.Unmarshal([]byte(data), &js); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	// Navigate the path
	parts := strings.Split(path, ".")
	current := js

	for _, part := range parts {
		// Handle array indices
		if strings.Contains(part, "[") && strings.Contains(part, "]") {
			base := strings.Split(part, "[")[0]
			indexStr := strings.Split(strings.Split(part, "[")[1], "]")[0]

			// Get base object
			if base != "" {
				obj, ok := current.(map[string]interface{})
				if !ok {
					return core.NewErrorResult(fmt.Sprintf("cannot access field '%s' on non-object", base)), nil
				}
				var exists bool
				current, exists = obj[base]
				if !exists {
					return core.NewErrorResult(fmt.Sprintf("field '%s' not found", base)), nil
				}
			}

			// Get array element
			arr, ok := current.([]interface{})
			if !ok {
				return core.NewErrorResult(fmt.Sprintf("cannot index non-array field '%s'", part)), nil
			}

			var index int
			if _, err := fmt.Sscanf(indexStr, "%d", &index); err != nil {
				return core.NewErrorResult(fmt.Sprintf("invalid array index: %s", indexStr)), nil
			}

			if index < 0 || index >= len(arr) {
				return core.NewErrorResult(fmt.Sprintf("array index %d out of bounds (length: %d)", index, len(arr))), nil
			}

			current = arr[index]
		} else {
			// Handle object field
			obj, ok := current.(map[string]interface{})
			if !ok {
				return core.NewErrorResult(fmt.Sprintf("cannot access field '%s' on non-object", part)), nil
			}

			var exists bool
			current, exists = obj[part]
			if !exists {
				return core.NewErrorResult(fmt.Sprintf("field '%s' not found", part)), nil
			}
		}
	}

	return core.NewResult(true, map[string]interface{}{
		"operation": "extract",
		"path":      path,
		"value":     current,
	}), nil
}

// merge merges two JSON objects.
func (t *JSONTools) merge(ctx context.Context, data1, data2 string) (core.Result, error) {
	var js1 interface{}
	if err := json.Unmarshal([]byte(data1), &js1); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid JSON in data: %v", err)), nil
	}

	var js2 interface{}
	if err := json.Unmarshal([]byte(data2), &js2); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid JSON in merge_data: %v", err)), nil
	}

	// Both should be objects for merge
	obj1, ok := js1.(map[string]interface{})
	if !ok {
		return core.NewErrorResult("data must be a JSON object for merge operation"), nil
	}

	obj2, ok := js2.(map[string]interface{})
	if !ok {
		return core.NewErrorResult("merge_data must be a JSON object for merge operation"), nil
	}

	// Deep merge
	merged := t.deepMerge(obj1, obj2)

	return core.NewResult(true, map[string]interface{}{
		"operation": "merge",
		"merged":    merged,
	}), nil
}

// deepMerge recursively merges two objects.
func (t *JSONTools) deepMerge(base, override map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	// Copy base
	for k, v := range base {
		result[k] = v
	}

	// Override with second object
	for k, v := range override {
		if baseVal, exists := result[k]; exists {
			// Both values are objects, merge recursively
			baseObj, ok1 := baseVal.(map[string]interface{})
			overrideObj, ok2 := v.(map[string]interface{})
			if ok1 && ok2 {
				result[k] = t.deepMerge(baseObj, overrideObj)
			} else {
				result[k] = v
			}
		} else {
			result[k] = v
		}
	}

	return result
}

// pretty pretty-prints JSON with indentation.
func (t *JSONTools) pretty(ctx context.Context, data, indent string) (core.Result, error) {
	var js interface{}
	if err := json.Unmarshal([]byte(data), &js); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	// Marshal with indentation
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indent)

	if err := encoder.Encode(js); err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to encode JSON: %v", err)), nil
	}

	// Remove trailing newline added by encoder
	pretty := strings.TrimSuffix(buf.String(), "\n")

	return core.NewResult(true, map[string]interface{}{
		"operation": "pretty",
		"pretty":    pretty,
	}), nil
}

// Helper functions.
func getString(params map[string]interface{}, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

func getStringSlice(params map[string]interface{}, key string) []string {
	if v, ok := params[key].([]interface{}); ok {
		result := make([]string, len(v))
		for i, val := range v {
			if s, ok := val.(string); ok {
				result[i] = s
			}
		}
		return result
	}
	return nil
}

func (t *JSONTools) IsIdempotent() bool { return true }

func getInt(params map[string]interface{}, key string, defaultVal int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultVal
}
