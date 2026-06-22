package builtin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"goagentx/internal/tools/resources/base"
	"goagentx/internal/tools/resources/core"
)

// Calculator performs mathematical calculations.
type Calculator struct {
	*base.BaseTool
}

// NewCalculator creates a new Calculator tool.
func NewCalculator() *Calculator {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"expression": {
				Type:        "string",
				Description: "A mathematical expression to evaluate. Examples: '100*(100+1)/2', '1000000*(1000000+1)/2', '5*6', '10/2'",
			},
		},
		Required: []string{"expression"},
	}

	return &Calculator{
		BaseTool: base.NewBaseToolWithCapabilities("calculator",
			"Evaluate mathematical expressions using standard arithmetic operations.\n\nIMPORTANT FORMULAS:\n- Sum from 1 to n: n*(n+1)/2\n- Sum from a to b: (b-a+1)*(a+b)/2\n\nSUPPORTED OPERATIONS:\n- Addition: +\n- Subtraction: -\n- Multiplication: *\n- Division: /\n- Parentheses: ()\n\nUSAGE RULES:\n- Always use mathematical expressions, not natural language\n- For '1 to 100 sum', use: 100*(100+1)/2\n- For '1 to 1000000 sum', use: 1000000*(1000000+1)/2\n\nExamples:\n- 1+2 → 1+2\n- 10*20 → 10*20\n- Sum 1 to 100 → 100*(100+1)/2\n- Sum 1 to 100000 → 100000*(100000+1)/2",
			core.CategoryCore, []core.Capability{core.CapabilityMath}, params),
	}
}

// Execute performs the calculation.
func (t *Calculator) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	expression, ok := params["expression"].(string)
	if !ok || expression == "" {
		return core.NewErrorResult("invalid_expression"), nil
	}

	// Evaluate the expression
	result, err := evaluateExpression(expression)
	if err != nil {
		return core.NewErrorResult("invalid_expression"), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"expression": expression,
		"result":     result,
	}), nil
}

// evaluateExpression evaluates a simple mathematical expression.
// Supports: +, -, *, /, (), and numbers (integers and floats)
func evaluateExpression(expr string) (float64, error) {
	// Remove whitespace
	expr = strings.ReplaceAll(expr, " ", "")

	if expr == "" {
		return 0, fmt.Errorf("empty expression")
	}

	// Parse and evaluate the expression
	return parseExpression(expr)
}

// parseExpression parses and evaluates an expression
func parseExpression(expr string) (float64, error) {
	return parseAddSub(expr)
}

// parseAddSub handles + and -
func parseAddSub(expr string) (float64, error) {
	left, remaining, err := parseMulDiv(expr)
	if err != nil {
		return 0, err
	}

	for len(remaining) > 0 {
		switch remaining[0] {
		case '+':
			right, newRemaining, err := parseMulDiv(remaining[1:])
			if err != nil {
				return 0, err
			}
			left += right
			remaining = newRemaining
		case '-':
			right, newRemaining, err := parseMulDiv(remaining[1:])
			if err != nil {
				return 0, err
			}
			left -= right
			remaining = newRemaining
		default:
			// Invalid character encountered
			return 0, fmt.Errorf("invalid character in expression: %c", remaining[0])
		}
	}

	return left, nil
}

// parseMulDiv handles * and /
func parseMulDiv(expr string) (float64, string, error) {
	left, remaining, err := parseFactor(expr)
	if err != nil {
		return 0, "", err
	}

loop:
	for len(remaining) > 0 {
		switch remaining[0] {
		case '*':
			right, newRemaining, err := parseFactor(remaining[1:])
			if err != nil {
				return 0, "", err
			}
			left *= right
			remaining = newRemaining
		case '/':
			right, newRemaining, err := parseFactor(remaining[1:])
			if err != nil {
				return 0, "", err
			}
			if right == 0 {
				return 0, "", fmt.Errorf("division by zero")
			}
			left /= right
			remaining = newRemaining
		default:
			break loop
		}
	}

	return left, remaining, nil
}

// parseFactor handles numbers and parentheses
func parseFactor(expr string) (float64, string, error) {
	if len(expr) == 0 {
		return 0, "", fmt.Errorf("unexpected end of expression")
	}

	// Handle parentheses
	if expr[0] == '(' {
		// Find matching closing parenthesis
		parenCount := 1
		end := 1
		for end < len(expr) && parenCount > 0 {
			switch expr[end] {
			case '(':
				parenCount++
			case ')':
				parenCount--
			}
			end++
		}

		if parenCount != 0 {
			return 0, "", fmt.Errorf("unmatched parentheses")
		}

		// Evaluate expression inside parentheses
		value, err := parseExpression(expr[1 : end-1])
		if err != nil {
			return 0, "", err
		}

		return value, expr[end:], nil
	}

	// Parse number
	return parseNumber(expr)
}

// parseNumber parses a number from the expression
func parseNumber(expr string) (float64, string, error) {
	i := 0

	// Handle optional negative sign
	if len(expr) > 0 && expr[0] == '-' {
		i++
	}

	// Parse digits and decimal point
	dotSeen := false
	for i < len(expr) {
		if expr[i] == '.' {
			if dotSeen {
				break
			}
			dotSeen = true
			i++
		} else if expr[i] >= '0' && expr[i] <= '9' {
			i++
		} else {
			break
		}
	}

	if i == 0 || (i == 1 && expr[0] == '-') {
		return 0, "", fmt.Errorf("expected number at position %d", i)
	}

	numStr := expr[:i]

	// Check if number ends with decimal point
	if numStr[len(numStr)-1] == '.' {
		return 0, "", fmt.Errorf("invalid number format: ends with decimal point")
	}

	value, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, "", fmt.Errorf("failed to parse number '%s': %v", numStr, err)
	}

	return value, expr[i:], nil
}

func (t *Calculator) IsIdempotent() bool { return true }

// DateTime provides date and time operations.
type DateTime struct {
	*base.BaseTool
}

// NewDateTime creates a new DateTime tool.
func NewDateTime() *DateTime {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (now, format, parse, add, diff)",
				Enum:        []interface{}{"now", "format", "parse", "add", "diff"},
			},
			"time_string": {
				Type:        "string",
				Description: "Time string for parse/format operations",
			},
			"format": {
				Type:        "string",
				Description: "Format string (e.g., '2006-01-02 15:04:05')",
			},
			"duration": {
				Type:        "string",
				Description: "Duration to add (e.g., '1h', '30m', '2d')",
			},
		},
		Required: []string{"operation"},
	}

	return &DateTime{
		BaseTool: base.NewBaseToolWithCapabilities("datetime", "Get current time and perform date/time operations", core.CategoryCore, []core.Capability{core.CapabilityTime}, params),
	}
}

// Execute performs the date/time operation.
func (t *DateTime) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	now := time.Now()

	switch operation {
	case "now":
		format := getString(params, "format")
		if format == "" {
			format = "2006-01-02 15:04:05"
		}
		return core.NewResult(true, map[string]interface{}{
			"formatted": now.Format(format),
			"unix":      now.Unix(),
			"unix_nano": now.UnixNano(),
		}), nil

	case "format":
		timeStr := getString(params, "time_string")
		if timeStr == "" {
			return core.NewErrorResult("time_string is required for format operation"), nil
		}
		format := getString(params, "format")
		if format == "" {
			format = "2006-01-02 15:04:05"
		}
		parsedTime, err := time.Parse(format, timeStr)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse time: %v", err)), nil
		}
		return core.NewResult(true, map[string]interface{}{
			"parsed":    parsedTime,
			"unix":      parsedTime.Unix(),
			"formatted": parsedTime.Format(format),
		}), nil

	case "parse":
		timeStr := getString(params, "time_string")
		if timeStr == "" {
			return core.NewErrorResult("time_string is required for parse operation"), nil
		}
		// Try common formats
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
			"2006/01/02 15:04:05",
			"2006/01/02",
		}
		var parsedTime time.Time
		var err error
		for _, format := range formats {
			parsedTime, err = time.Parse(format, timeStr)
			if err == nil {
				break
			}
		}
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse time with common formats: %v", err)), nil
		}
		return core.NewResult(true, map[string]interface{}{
			"parsed": parsedTime,
			"unix":   parsedTime.Unix(),
		}), nil

	case "add":
		durationStr := getString(params, "duration")
		if durationStr == "" {
			return core.NewErrorResult("duration is required for add operation"), nil
		}
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse duration: %v", err)), nil
		}
		result := now.Add(duration)
		return core.NewResult(true, map[string]interface{}{
			"original": now,
			"duration": durationStr,
			"result":   result,
			"unix":     result.Unix(),
		}), nil

	case "diff":
		timeStr := getString(params, "time_string")
		if timeStr == "" {
			return core.NewErrorResult("time_string is required for diff operation"), nil
		}
		// Parse time
		var parsedTime time.Time
		var err error
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
		}
		for _, format := range formats {
			parsedTime, err = time.Parse(format, timeStr)
			if err == nil {
				break
			}
		}
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse time: %v", err)), nil
		}
		diff := now.Sub(parsedTime)
		return core.NewResult(true, map[string]interface{}{
			"now":      now,
			"target":   parsedTime,
			"duration": diff,
			"seconds":  diff.Seconds(),
			"minutes":  diff.Minutes(),
			"hours":    diff.Hours(),
			"days":     diff.Hours() / 24,
		}), nil

	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

func (t *DateTime) IsIdempotent() bool { return true }

// TextProcessor provides text processing operations.
type TextProcessor struct {
	*base.BaseTool
}

// NewTextProcessor creates a new TextProcessor tool.
func NewTextProcessor() *TextProcessor {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (count, split, replace, uppercase, lowercase, trim, contains)",
				Enum:        []interface{}{"count", "split", "replace", "uppercase", "lowercase", "trim", "contains"},
			},
			"text": {
				Type:        "string",
				Description: "Text to process",
			},
			"separator": {
				Type:        "string",
				Description: "Separator for split operation",
			},
			"old": {
				Type:        "string",
				Description: "Old substring to replace",
			},
			"new": {
				Type:        "string",
				Description: "New substring",
			},
			"substring": {
				Type:        "string",
				Description: "Substring to check for contains operation",
			},
		},
		Required: []string{"operation", "text"},
	}

	return &TextProcessor{
		BaseTool: base.NewBaseToolWithCapabilities("text_processor", "Perform text processing operations", core.CategoryCore, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the text processing operation.
func (t *TextProcessor) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	text, ok := params["text"].(string)
	if !ok {
		return core.NewErrorResult("text is required"), nil
	}

	switch operation {
	case "count":
		return core.NewResult(true, map[string]interface{}{
			"length": len(text),
			"chars":  len([]rune(text)),
			"words":  len(strings.Fields(text)),
			"lines":  len(strings.Split(text, "\n")),
		}), nil

	case "split":
		separator := getString(params, "separator")
		if separator == "" {
			separator = " "
		}
		parts := strings.Split(text, separator)
		return core.NewResult(true, map[string]interface{}{
			"parts": parts,
			"count": len(parts),
		}), nil

	case "replace":
		oldStr := getString(params, "old")
		newStr := getString(params, "new")
		if oldStr == "" {
			return core.NewErrorResult("old substring is required for replace operation"), nil
		}
		result := strings.ReplaceAll(text, oldStr, newStr)
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   result,
		}), nil

	case "uppercase":
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   strings.ToUpper(text),
		}), nil

	case "lowercase":
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   strings.ToLower(text),
		}), nil

	case "trim":
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   strings.TrimSpace(text),
		}), nil

	case "contains":
		substring := getString(params, "substring")
		if substring == "" {
			return core.NewErrorResult("substring is required for contains operation"), nil
		}
		contains := strings.Contains(text, substring)
		return core.NewResult(true, map[string]interface{}{
			"contains": contains,
		}), nil

	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

func (t *TextProcessor) IsIdempotent() bool { return true }

// getString safely gets a string parameter.
func getString(params map[string]interface{}, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}
