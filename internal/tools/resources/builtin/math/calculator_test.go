package builtin

import (
	"context"
	"testing"

	"github.com/Timwood0x10/goagent/internal/tools/resources/core"
)

// TestNewCalculator tests creating a new Calculator tool.
func TestNewCalculator(t *testing.T) {
	calculator := NewCalculator()
	if calculator == nil {
		t.Fatal("NewCalculator() should not return nil")
	}
	if calculator.Name() != "calculator" {
		t.Errorf("Name() = %q, want 'calculator'", calculator.Name())
	}
	if calculator.Category() != core.CategoryCore {
		t.Errorf("Category() = %v, want CategoryCore", calculator.Category())
	}
}

// TestCalculatorExecute_BasicOperations tests basic arithmetic operations.
func TestCalculatorExecute_BasicOperations(t *testing.T) {
	calculator := NewCalculator()
	ctx := context.Background()

	tests := []struct {
		name       string
		expression string
		wantResult float64
		wantError  bool
	}{
		{
			name:       "addition",
			expression: "1+2",
			wantResult: 3.0,
			wantError:  false,
		},
		{
			name:       "subtraction",
			expression: "10-5",
			wantResult: 5.0,
			wantError:  false,
		},
		{
			name:       "multiplication",
			expression: "5*6",
			wantResult: 30.0,
			wantError:  false,
		},
		{
			name:       "division",
			expression: "10/2",
			wantResult: 5.0,
			wantError:  false,
		},
		{
			name:       "float division",
			expression: "10/3",
			wantResult: 10.0 / 3.0,
			wantError:  false,
		},
		{
			name:       "float numbers",
			expression: "1.5+2.5",
			wantResult: 4.0,
			wantError:  false,
		},
		{
			name:       "negative number",
			expression: "-5+10",
			wantResult: 5.0,
			wantError:  false,
		},
		{
			name:       "multiple operations",
			expression: "1+2+3+4",
			wantResult: 10.0,
			wantError:  false,
		},
		{
			name:       "mixed operations",
			expression: "10-5+3",
			wantResult: 8.0,
			wantError:  false,
		},
		{
			name:       "multiplication before addition",
			expression: "2+3*4",
			wantResult: 14.0,
			wantError:  false,
		},
		{
			name:       "division before addition",
			expression: "10+20/5",
			wantResult: 14.0,
			wantError:  false,
		},
		{
			name:       "complex expression",
			expression: "100*(100+1)/2",
			wantResult: 5050.0,
			wantError:  false,
		},
		{
			name:       "sum formula 1 to 100",
			expression: "100*(100+1)/2",
			wantResult: 5050.0,
			wantError:  false,
		},
		{
			name:       "sum formula 1 to 1000000",
			expression: "1000000*(1000000+1)/2",
			wantResult: 500000500000.0,
			wantError:  false,
		},
		{
			name:       "parentheses override precedence",
			expression: "(2+3)*4",
			wantResult: 20.0,
			wantError:  false,
		},
		{
			name:       "nested parentheses",
			expression: "((2+3)*4)/2",
			wantResult: 10.0,
			wantError:  false,
		},
		{
			name:       "expression with spaces",
			expression: "100 * (100 + 1) / 2",
			wantResult: 5050.0,
			wantError:  false,
		},
		{
			name:       "complex nested",
			expression: "((10+20)*5-50)/2",
			wantResult: 50.0,
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"expression": tt.expression,
			}

			result, err := calculator.Execute(ctx, params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success != !tt.wantError {
				t.Errorf("Execute() Success = %v, want %v", result.Success, !tt.wantError)
			}

			if !tt.wantError {
				data, ok := result.Data.(map[string]interface{})
				if !ok {
					t.Error("Result.Data should be map[string]interface{}")
					return
				}

				resultValue, ok := data["result"].(float64)
				if !ok {
					t.Error("result should be float64")
					return
				}

				if resultValue != tt.wantResult {
					t.Errorf("Execute() result = %v, want %v", resultValue, tt.wantResult)
				}
			}
		})
	}
}

// TestCalculatorExecute_DivisionByZero tests division by zero.
func TestCalculatorExecute_DivisionByZero(t *testing.T) {
	calculator := NewCalculator()
	ctx := context.Background()

	tests := []struct {
		name       string
		expression string
	}{
		{
			name:       "simple division by zero",
			expression: "10/0",
		},
		{
			name:       "division by zero in expression",
			expression: "10/0+5",
		},
		{
			name:       "division by zero with parentheses",
			expression: "(10+5)/0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"expression": tt.expression,
			}

			result, err := calculator.Execute(ctx, params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success {
				t.Error("Execute() should fail for division by zero")
			}

			if result.Error != "invalid_expression" {
				t.Errorf("Execute() Error = %q, want 'invalid_expression'", result.Error)
			}
		})
	}
}

// TestCalculatorExecute_InvalidInput tests invalid input handling.
func TestCalculatorExecute_InvalidInput(t *testing.T) {
	calculator := NewCalculator()
	ctx := context.Background()

	tests := []struct {
		name       string
		expression string
	}{
		{
			name:       "empty expression",
			expression: "",
		},
		{
			name:       "whitespace only",
			expression: "   ",
		},
		{
			name:       "unmatched opening parenthesis",
			expression: "(1+2",
		},
		{
			name:       "unmatched closing parenthesis",
			expression: "1+2)",
		},
		{
			name:       "invalid character",
			expression: "1+2a",
		},
		{
			name:       "missing operator with decimal",
			expression: "1.2.3",
		},
		{
			name:       "consecutive operators",
			expression: "1++2",
		},
		{
			name:       "starting with operator",
			expression: "+5",
		},
		{
			name:       "ending with operator",
			expression: "5+",
		},
		{
			name:       "multiple decimal points",
			expression: "1.2.3",
		},
		{
			name:       "decimal point without digits",
			expression: "5.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"expression": tt.expression,
			}

			result, err := calculator.Execute(ctx, params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success {
				t.Error("Execute() should fail for invalid expression")
			}

			if result.Error != "invalid_expression" {
				t.Errorf("Execute() Error = %q, want 'invalid_expression'", result.Error)
			}
		})
	}
}

// TestCalculatorExecute_MissingExpression tests missing expression parameter.
func TestCalculatorExecute_MissingExpression(t *testing.T) {
	calculator := NewCalculator()
	ctx := context.Background()

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{
			name:   "no expression",
			params: map[string]interface{}{},
		},
		{
			name: "expression is not string",
			params: map[string]interface{}{
				"expression": 123,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := calculator.Execute(ctx, tt.params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success {
				t.Error("Execute() should fail when expression is missing or invalid")
			}

			if result.Error != "invalid_expression" {
				t.Errorf("Execute() Error = %q, want 'invalid_expression'", result.Error)
			}
		})
	}
}

// TestCalculatorCapabilities tests calculator capabilities.
func TestCalculatorCapabilities(t *testing.T) {
	calculator := NewCalculator()

	capabilities := calculator.Capabilities()
	if len(capabilities) != 1 {
		t.Errorf("Capabilities() length = %d, want 1", len(capabilities))
	}

	if capabilities[0] != core.CapabilityMath {
		t.Errorf("Capabilities()[0] = %v, want CapabilityMath", capabilities[0])
	}

	parameters := calculator.Parameters()
	if parameters == nil {
		t.Fatal("Parameters() should not be nil")
	}

	if parameters.Type != "object" {
		t.Errorf("Parameters.Type = %q, want 'object'", parameters.Type)
	}

	exprParam, exists := parameters.Properties["expression"]
	if !exists {
		t.Error("expression parameter should exist")
	}

	if exprParam.Type != "string" {
		t.Errorf("expression parameter Type = %q, want 'string'", exprParam.Type)
	}

	if len(parameters.Required) != 1 || parameters.Required[0] != "expression" {
		t.Errorf("parameters.Required = %v, want [expression]", parameters.Required)
	}
}

// TestNewDateTime tests creating a new DateTime tool.
func TestNewDateTime(t *testing.T) {
	dateTime := NewDateTime()
	if dateTime == nil {
		t.Fatal("NewDateTime() should not return nil")
	}
	if dateTime.Name() != "datetime" {
		t.Errorf("Name() = %q, want 'datetime'", dateTime.Name())
	}
	if dateTime.Category() != core.CategoryCore {
		t.Errorf("Category() = %v, want CategoryCore", dateTime.Category())
	}
}

// TestDateTimeExecute_Operations tests DateTime operations.
func TestDateTimeExecute_Operations(t *testing.T) {
	dateTime := NewDateTime()
	ctx := context.Background()

	tests := []struct {
		name       string
		params     map[string]interface{}
		wantError  bool
		checkField string
	}{
		{
			name: "now operation",
			params: map[string]interface{}{
				"operation": "now",
			},
			wantError:  false,
			checkField: "formatted",
		},
		{
			name: "now with custom format",
			params: map[string]interface{}{
				"operation": "now",
				"format":    "2006-01-02",
			},
			wantError:  false,
			checkField: "formatted",
		},
		{
			name: "format operation",
			params: map[string]interface{}{
				"operation":   "format",
				"time_string": "2026-03-24 12:00:00",
				"format":      "2006-01-02 15:04:05",
			},
			wantError:  false,
			checkField: "parsed",
		},
		{
			name: "parse operation",
			params: map[string]interface{}{
				"operation":   "parse",
				"time_string": "2026-03-24T12:00:00Z",
			},
			wantError:  false,
			checkField: "parsed",
		},
		{
			name: "add operation",
			params: map[string]interface{}{
				"operation": "add",
				"duration":  "1h",
			},
			wantError:  false,
			checkField: "result",
		},
		{
			name: "add with complex duration",
			params: map[string]interface{}{
				"operation": "add",
				"duration":  "2h30m",
			},
			wantError:  false,
			checkField: "result",
		},
		{
			name: "diff operation",
			params: map[string]interface{}{
				"operation":   "diff",
				"time_string": "2026-03-24 10:00:00",
			},
			wantError:  false,
			checkField: "duration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := dateTime.Execute(ctx, tt.params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success != !tt.wantError {
				t.Errorf("Execute() Success = %v, want %v", result.Success, !tt.wantError)
			}

			if !tt.wantError {
				data, ok := result.Data.(map[string]interface{})
				if !ok {
					t.Error("Result.Data should be map[string]interface{}")
					return
				}

				if _, exists := data[tt.checkField]; !exists {
					t.Errorf("Result.Data should contain field %q", tt.checkField)
				}
			}
		})
	}
}

// TestDateTimeExecute_InvalidInput tests invalid input handling.
func TestDateTimeExecute_InvalidInput(t *testing.T) {
	dateTime := NewDateTime()
	ctx := context.Background()

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{
			name:   "missing operation",
			params: map[string]interface{}{},
		},
		{
			name: "empty operation",
			params: map[string]interface{}{
				"operation": "",
			},
		},
		{
			name: "invalid operation",
			params: map[string]interface{}{
				"operation": "invalid",
			},
		},
		{
			name: "format without time_string",
			params: map[string]interface{}{
				"operation": "format",
			},
		},
		{
			name: "parse without time_string",
			params: map[string]interface{}{
				"operation": "parse",
			},
		},
		{
			name: "add without duration",
			params: map[string]interface{}{
				"operation": "add",
			},
		},
		{
			name: "diff without time_string",
			params: map[string]interface{}{
				"operation": "diff",
			},
		},
		{
			name: "add with invalid duration",
			params: map[string]interface{}{
				"operation": "add",
				"duration":  "invalid",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := dateTime.Execute(ctx, tt.params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success {
				t.Error("Execute() should fail for invalid input")
			}
		})
	}
}

// TestNewTextProcessor tests creating a new TextProcessor tool.
func TestNewTextProcessor(t *testing.T) {
	textProcessor := NewTextProcessor()
	if textProcessor == nil {
		t.Fatal("NewTextProcessor() should not return nil")
	}
	if textProcessor.Name() != "text_processor" {
		t.Errorf("Name() = %q, want 'text_processor'", textProcessor.Name())
	}
	if textProcessor.Category() != core.CategoryCore {
		t.Errorf("Category() = %v, want CategoryCore", textProcessor.Category())
	}
}

// TestTextProcessorExecute_Operations tests TextProcessor operations.
func TestTextProcessorExecute_Operations(t *testing.T) {
	textProcessor := NewTextProcessor()
	ctx := context.Background()

	tests := []struct {
		name       string
		params     map[string]interface{}
		wantError  bool
		checkField string
	}{
		{
			name: "count operation",
			params: map[string]interface{}{
				"operation": "count",
				"text":      "Hello World",
			},
			wantError:  false,
			checkField: "length",
		},
		{
			name: "count with multibyte characters",
			params: map[string]interface{}{
				"operation": "count",
				"text":      "你好世界",
			},
			wantError:  false,
			checkField: "chars",
		},
		{
			name: "split operation",
			params: map[string]interface{}{
				"operation": "split",
				"text":      "a,b,c",
				"separator": ",",
			},
			wantError:  false,
			checkField: "parts",
		},
		{
			name: "split with default separator",
			params: map[string]interface{}{
				"operation": "split",
				"text":      "a b c",
			},
			wantError:  false,
			checkField: "parts",
		},
		{
			name: "replace operation",
			params: map[string]interface{}{
				"operation": "replace",
				"text":      "Hello World",
				"old":       "World",
				"new":       "Go",
			},
			wantError:  false,
			checkField: "result",
		},
		{
			name: "uppercase operation",
			params: map[string]interface{}{
				"operation": "uppercase",
				"text":      "hello",
			},
			wantError:  false,
			checkField: "result",
		},
		{
			name: "lowercase operation",
			params: map[string]interface{}{
				"operation": "lowercase",
				"text":      "HELLO",
			},
			wantError:  false,
			checkField: "result",
		},
		{
			name: "trim operation",
			params: map[string]interface{}{
				"operation": "trim",
				"text":      "  hello  ",
			},
			wantError:  false,
			checkField: "result",
		},
		{
			name: "contains operation - true",
			params: map[string]interface{}{
				"operation": "contains",
				"text":      "Hello World",
				"substring": "World",
			},
			wantError:  false,
			checkField: "contains",
		},
		{
			name: "contains operation - false",
			params: map[string]interface{}{
				"operation": "contains",
				"text":      "Hello World",
				"substring": "Go",
			},
			wantError:  false,
			checkField: "contains",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := textProcessor.Execute(ctx, tt.params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success != !tt.wantError {
				t.Errorf("Execute() Success = %v, want %v", result.Success, !tt.wantError)
			}

			if !tt.wantError {
				data, ok := result.Data.(map[string]interface{})
				if !ok {
					t.Error("Result.Data should be map[string]interface{}")
					return
				}

				if _, exists := data[tt.checkField]; !exists {
					t.Errorf("Result.Data should contain field %q", tt.checkField)
				}
			}
		})
	}
}

// TestTextProcessorExecute_InvalidInput tests invalid input handling.
func TestTextProcessorExecute_InvalidInput(t *testing.T) {
	textProcessor := NewTextProcessor()
	ctx := context.Background()

	tests := []struct {
		name   string
		params map[string]interface{}
	}{
		{
			name:   "missing operation",
			params: map[string]interface{}{},
		},
		{
			name: "empty operation",
			params: map[string]interface{}{
				"operation": "",
			},
		},
		{
			name: "invalid operation",
			params: map[string]interface{}{
				"operation": "invalid",
			},
		},
		{
			name: "missing text",
			params: map[string]interface{}{
				"operation": "uppercase",
			},
		},
		{
			name: "text is not string",
			params: map[string]interface{}{
				"operation": "uppercase",
				"text":      123,
			},
		},
		{
			name: "replace without old",
			params: map[string]interface{}{
				"operation": "replace",
				"text":      "Hello",
			},
		},
		{
			name: "contains without substring",
			params: map[string]interface{}{
				"operation": "contains",
				"text":      "Hello",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := textProcessor.Execute(ctx, tt.params)
			if err != nil {
				t.Errorf("Execute() unexpected error: %v", err)
				return
			}

			if result.Success {
				t.Error("Execute() should fail for invalid input")
			}
		})
	}
}
