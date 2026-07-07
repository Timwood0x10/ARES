package builtin

import (
	"context"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// StringUtils provides string manipulation operations.
type StringUtils struct {
	*base.BaseTool
}

// NewStringUtils creates a new StringUtils tool.
func NewStringUtils() *StringUtils {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation: upper, lower, trim, split, join, length, reverse, substring, replace",
				Enum:        []interface{}{"upper", "lower", "trim", "split", "join", "length", "reverse", "substring", "replace"},
			},
			"input": {
				Type:        "string",
				Description: "Input text to process",
			},
			"delimiter": {
				Type:        "string",
				Description: "Delimiter for split/join operations",
			},
			"join_items": {
				Type:        "string",
				Description: "Array items to join (comma-separated, used with join operation)",
			},
			"start": {
				Type:        "integer",
				Description: "Start index for substring (0-based, inclusive)",
			},
			"end": {
				Type:        "integer",
				Description: "End index for substring (0-based, exclusive)",
			},
			"old": {
				Type:        "string",
				Description: "String to replace (used with replace operation)",
			},
			"new": {
				Type:        "string",
				Description: "Replacement string (used with replace operation)",
			},
		},
		Required: []string{"operation", "input"},
	}

	return &StringUtils{
		BaseTool: base.NewBaseToolWithCapabilities("string_utils",
			"String manipulation: case conversion, trim, split, join, length, reverse, substring, replace",
			core.CategoryCore, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the string operation.
func (t *StringUtils) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	input, ok := params["input"].(string)
	if !ok {
		return core.NewErrorResult("input is required"), nil
	}

	switch operation {
	case "upper":
		return core.NewResult(true, map[string]interface{}{
			"operation": "upper", "input": input, "output": strings.ToUpper(input),
		}), nil
	case "lower":
		return core.NewResult(true, map[string]interface{}{
			"operation": "lower", "input": input, "output": strings.ToLower(input),
		}), nil
	case "trim":
		return core.NewResult(true, map[string]interface{}{
			"operation": "trim", "input": input, "output": strings.TrimSpace(input),
		}), nil
	case "length":
		return core.NewResult(true, map[string]interface{}{
			"operation": "length", "input": input, "output": len([]rune(input)),
		}), nil
	case "reverse":
		runes := []rune(input)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return core.NewResult(true, map[string]interface{}{
			"operation": "reverse", "input": input, "output": string(runes),
		}), nil
	case "split":
		delimiter, _ := params["delimiter"].(string)
		if delimiter == "" {
			delimiter = ","
		}
		parts := strings.Split(input, delimiter)
		result := make([]interface{}, len(parts))
		for i, p := range parts {
			result[i] = strings.TrimSpace(p)
		}
		return core.NewResult(true, map[string]interface{}{
			"operation": "split", "input": input, "delimiter": delimiter, "output": result, "count": len(parts),
		}), nil
	case "substring":
		start := getInt(params, "start", 0)
		end := getInt(params, "end", len([]rune(input)))
		runes := []rune(input)
		if start < 0 || start > len(runes) || end < start || end > len(runes) {
			return core.NewErrorResult(fmt.Sprintf("invalid substring range [%d:%d], length is %d", start, end, len(runes))), nil
		}
		return core.NewResult(true, map[string]interface{}{
			"operation": "substring", "input": input, "start": start, "end": end, "output": string(runes[start:end]),
		}), nil
	case "join":
		delimiter, _ := params["delimiter"].(string)
		if delimiter == "" {
			delimiter = ","
		}
		joinItemsStr, _ := params["join_items"].(string)
		if joinItemsStr == "" {
			return core.NewErrorResult("join_items is required for join operation"), nil
		}
		items := strings.Split(joinItemsStr, ",")
		trimmed := make([]string, len(items))
		for i, item := range items {
			trimmed[i] = strings.TrimSpace(item)
		}
		return core.NewResult(true, map[string]interface{}{
			"operation": "join", "delimiter": delimiter, "output": strings.Join(trimmed, delimiter),
		}), nil
	case "replace":
		oldStr, _ := params["old"].(string)
		newStr, _ := params["new"].(string)
		if oldStr == "" {
			return core.NewErrorResult("old string is required for replace operation"), nil
		}
		return core.NewResult(true, map[string]interface{}{
			"operation": "replace", "input": input, "old": oldStr, "new": newStr,
			"output": strings.ReplaceAll(input, oldStr, newStr),
		}), nil
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// IsIdempotent returns true since string operations have no side effects.
func (t *StringUtils) IsIdempotent() bool { return true }

// getInt safely extracts an int parameter from the params map.
func getInt(params map[string]interface{}, key string, defaultVal int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		if i, err := parseInt(v); err == nil {
			return i
		}
	}
	return defaultVal
}

// parseInt converts a string to int.
func parseInt(s string) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
