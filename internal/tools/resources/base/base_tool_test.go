package base

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"goagentx/internal/tools/resources/core"
)

// TestNewBaseTool tests creating a new BaseTool.
func TestNewBaseTool(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		description string
		params      *core.ParameterSchema
	}{
		{
			name:        "tool with parameters",
			toolName:    "test_tool",
			description: "A test tool",
			params: &core.ParameterSchema{
				Type: "object",
				Properties: map[string]*core.Parameter{
					"param1": {
						Type:        "string",
						Description: "A parameter",
					},
				},
				Required: []string{"param1"},
			},
		},
		{
			name:        "tool without parameters",
			toolName:    "simple_tool",
			description: "A simple tool",
			params:      nil,
		},
		{
			name:        "tool with empty name",
			toolName:    "",
			description: "Tool with empty name",
			params:      nil,
		},
		{
			name:        "tool with empty description",
			toolName:    "no_desc_tool",
			description: "",
			params:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewBaseTool(tt.toolName, tt.description, tt.params)

			if tool == nil {
				t.Fatal("NewBaseTool() should not return nil")
			}

			if tool.Name() != tt.toolName {
				t.Errorf("Name() = %q, want %q", tool.Name(), tt.toolName)
			}

			if tool.Description() != tt.description {
				t.Errorf("Description() = %q, want %q", tool.Description(), tt.description)
			}

			if tool.Category() != core.CategoryCore {
				t.Errorf("Category() = %q, want %q", tool.Category(), core.CategoryCore)
			}

			if len(tool.Capabilities()) != 0 {
				t.Errorf("Capabilities() should return empty slice, got %d", len(tool.Capabilities()))
			}

			if tool.Parameters() != tt.params {
				t.Error("Parameters() should return the provided schema")
			}

			if tool.Metadata() != nil {
				t.Error("Metadata() should return nil for new tool")
			}
		})
	}
}

// TestNewBaseToolWithCategory tests creating a BaseTool with specific category.
func TestNewBaseToolWithCategory(t *testing.T) {
	tests := []struct {
		name     string
		category core.ToolCategory
	}{
		{
			name:     "system category",
			category: core.CategorySystem,
		},
		{
			name:     "core category",
			category: core.CategoryCore,
		},
		{
			name:     "data category",
			category: core.CategoryData,
		},
		{
			name:     "knowledge category",
			category: core.CategoryKnowledge,
		},
		{
			name:     "memory category",
			category: core.CategoryMemory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewBaseToolWithCategory("test_tool", "A test tool", tt.category, nil)

			if tool == nil {
				t.Fatal("NewBaseToolWithCategory() should not return nil")
			}

			if tool.Category() != tt.category {
				t.Errorf("Category() = %q, want %q", tool.Category(), tt.category)
			}
		})
	}
}

// TestNewBaseToolWithCapabilities tests creating a BaseTool with specific capabilities.
func TestNewBaseToolWithCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		capabilities []core.Capability
	}{
		{
			name: "single capability",
			capabilities: []core.Capability{
				core.CapabilityMath,
			},
		},
		{
			name: "multiple capabilities",
			capabilities: []core.Capability{
				core.CapabilityMath,
				core.CapabilityText,
			},
		},
		{
			name:         "empty capabilities",
			capabilities: []core.Capability{},
		},
		{
			name:         "nil capabilities",
			capabilities: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewBaseToolWithCapabilities(
				"test_tool",
				"A test tool",
				core.CategoryCore,
				tt.capabilities,
				nil,
			)

			if tool == nil {
				t.Fatal("NewBaseToolWithCapabilities() should not return nil")
			}

			caps := tool.Capabilities()
			if tt.capabilities == nil {
				if caps != nil {
					t.Error("Capabilities() should return nil for nil input")
				}
			} else {
				if len(caps) != len(tt.capabilities) {
					t.Errorf("Capabilities() length = %d, want %d", len(caps), len(tt.capabilities))
				}

				// Verify capabilities match
				capSet := make(map[core.Capability]bool)
				for _, cap := range caps {
					capSet[cap] = true
				}

				for _, expectedCap := range tt.capabilities {
					if !capSet[expectedCap] {
						t.Errorf("capability %q not found in tool capabilities", expectedCap)
					}
				}
			}
		})
	}
}

// TestBaseToolMethods tests BaseTool getter methods.
func TestBaseToolMethods(t *testing.T) {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"input": {
				Type:        "string",
				Description: "Input parameter",
			},
		},
		Required: []string{"input"},
	}

	tool := NewBaseToolWithCapabilities(
		"method_test_tool",
		"A tool for method testing",
		core.CategorySystem,
		[]core.Capability{core.CapabilityFile},
		params,
	)

	// Test Name()
	if tool.Name() != "method_test_tool" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "method_test_tool")
	}

	// Test Description()
	if tool.Description() != "A tool for method testing" {
		t.Errorf("Description() = %q, want %q", tool.Description(), "A tool for method testing")
	}

	// Test Category()
	if tool.Category() != core.CategorySystem {
		t.Errorf("Category() = %q, want %q", tool.Category(), core.CategorySystem)
	}

	// Test Capabilities()
	caps := tool.Capabilities()
	if len(caps) != 1 {
		t.Fatalf("Capabilities() length = %d, want 1", len(caps))
	}
	if caps[0] != core.CapabilityFile {
		t.Errorf("Capabilities()[0] = %q, want %q", caps[0], core.CapabilityFile)
	}

	// Test Parameters()
	if tool.Parameters() != params {
		t.Error("Parameters() should return the provided schema")
	}

	// Test Metadata()
	if tool.Metadata() != nil {
		t.Error("Metadata() should return nil")
	}
}

// TestNewToolFunc tests creating a ToolFunc.
func TestNewToolFunc(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		description string
		params      *core.ParameterSchema
		fn          func(ctx context.Context, params map[string]interface{}) (core.Result, error)
	}{
		{
			name:        "function tool with parameters",
			toolName:    "func_tool",
			description: "A function tool",
			params: &core.ParameterSchema{
				Type: "object",
				Properties: map[string]*core.Parameter{
					"value": {
						Type:        "number",
						Description: "A numeric value",
					},
				},
			},
			fn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
				return core.NewResult(true, params), nil
			},
		},
		{
			name:        "function tool without parameters",
			toolName:    "simple_func_tool",
			description: "A simple function tool",
			params:      nil,
			fn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
				return core.NewResult(true, "executed"), nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewToolFunc(tt.toolName, tt.description, tt.params, tt.fn)

			require.NotNil(t, tool)

			if tool.Name() != tt.toolName {
				t.Errorf("Name() = %q, want %q", tool.Name(), tt.toolName)
			}

			if tool.Description() != tt.description {
				t.Errorf("Description() = %q, want %q", tool.Description(), tt.description)
			}

			if tool.Category() != core.CategoryCore {
				t.Errorf("Category() = %q, want %q", tool.Category(), core.CategoryCore)
			}

			if tool.Parameters() != tt.params {
				t.Error("Parameters() should return the provided schema")
			}
		})
	}
}

// TestToolFuncExecute tests executing a ToolFunc.
func TestToolFuncExecute(t *testing.T) {
	tests := []struct {
		name     string
		fn       func(ctx context.Context, params map[string]interface{}) (core.Result, error)
		params   map[string]interface{}
		wantErr  bool
		wantData interface{}
	}{
		{
			name: "successful execution",
			fn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
				return core.NewResult(true, map[string]interface{}{
					"result": "success",
					"params": params,
				}), nil
			},
			params: map[string]interface{}{
				"key": "value",
			},
			wantErr: false,
			wantData: map[string]interface{}{
				"result": "success",
				"params": map[string]interface{}{
					"key": "value",
				},
			},
		},
		{
			name: "execution with error",
			fn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
				return core.Result{}, context.DeadlineExceeded
			},
			params:  map[string]interface{}{},
			wantErr: true,
		},
		{
			name: "execution with nil params",
			fn: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
				return core.NewResult(true, "executed"), nil
			},
			params:   nil,
			wantErr:  false,
			wantData: "executed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := NewToolFunc("test_tool", "Test tool", nil, tt.fn)

			ctx := context.Background()
			result, err := tool.Execute(ctx, tt.params)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}

				if !result.Success {
					t.Error("result should be successful")
				}

				if tt.wantData != nil {
					// Simple comparison for basic types
					switch expected := tt.wantData.(type) {
					case string:
						if result.Data != expected {
							t.Errorf("result data = %v, want %v", result.Data, expected)
						}
					case map[string]interface{}:
						dataMap, ok := result.Data.(map[string]interface{})
						if !ok {
							t.Error("result data should be a map")
						} else {
							for key, value := range expected {
								if !reflect.DeepEqual(dataMap[key], value) {
									t.Errorf("result data[%q] = %v, want %v", key, dataMap[key], value)
								}
							}
						}
					}
				}
			}
		})
	}
}

// TestToolFuncExecuteWithContext tests ToolFunc execution with context.
func TestToolFuncExecuteWithContext(t *testing.T) {
	tool := NewToolFunc(
		"context_tool",
		"A tool that uses context",
		nil,
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			// Check if context is valid
			if ctx == nil {
				return core.Result{}, context.Canceled
			}

			// Simulate some work
			select {
			case <-ctx.Done():
				return core.Result{}, ctx.Err()
			default:
				return core.NewResult(true, "completed"), nil
			}
		},
	)

	// Test with valid context
	ctx := context.Background()
	result, err := tool.Execute(ctx, map[string]interface{}{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("result should be successful")
	}

	// Test with cancelled context
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = tool.Execute(cancelCtx, map[string]interface{}{})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// TestWithMetadata tests adding metadata to a tool.
func TestWithMetadata(t *testing.T) {
	baseTool := NewToolFunc(
		"metadata_tool",
		"A tool with metadata",
		nil,
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.NewResult(true, nil), nil
		},
	)

	metadata := core.ToolMetadata{
		Version:     "1.0.0",
		Author:      "test-author",
		Tags:        []string{"tag1", "tag2"},
		Examples:    []string{"example1"},
		Deprecated:  false,
		Deprecation: "",
	}

	toolWithMeta := WithMetadata(baseTool, metadata)

	if toolWithMeta == nil {
		t.Fatal("WithMetadata() should not return nil")
	}

	// Verify tool properties are preserved
	if toolWithMeta.Name() != "metadata_tool" {
		t.Errorf("Name() = %q, want %q", toolWithMeta.Name(), "metadata_tool")
	}

	if toolWithMeta.Description() != "A tool with metadata" {
		t.Errorf("Description() = %q, want %q", toolWithMeta.Description(), "A tool with metadata")
	}

	// Test IsDeprecated method - metadataTool is unexported, so we test via interface
	// The tool should still work as a Tool interface
	ctx := context.Background()
	result, err := toolWithMeta.Execute(ctx, map[string]interface{}{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("result should be successful")
	}

	// Test with deprecated metadata
	deprecatedMeta := core.ToolMetadata{
		Version:     "1.0.0",
		Deprecated:  true,
		Deprecation: "Use new_tool instead",
	}

	deprecatedTool := WithMetadata(baseTool, deprecatedMeta)

	// Verify the tool still works
	result, err = deprecatedTool.Execute(ctx, map[string]interface{}{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("result should be successful")
	}
}

// TestBaseToolImplementsToolInterface tests that BaseTool implements Tool interface.
func TestBaseToolImplementsToolInterface(t *testing.T) {
	// BaseTool doesn't implement Execute method, so it doesn't implement Tool interface
	// This is by design - BaseTool is a base class for other tools
	tool := NewBaseTool("interface_test", "Test tool", nil)

	// Verify it has the expected methods
	if tool.Name() != "interface_test" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "interface_test")
	}

	if tool.Description() != "Test tool" {
		t.Errorf("Description() = %q, want %q", tool.Description(), "Test tool")
	}
}

// TestToolFuncImplementsToolInterface tests that ToolFunc implements Tool interface.
func TestToolFuncImplementsToolInterface(t *testing.T) {
	tool := NewToolFunc("interface_test", "Test tool", nil, func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
		return core.NewResult(true, nil), nil
	})

	// This should compile if ToolFunc implements Tool interface
	var _ core.Tool = tool
}

// TestMetadataToolImplementsToolInterface tests that metadataTool implements Tool interface.
func TestMetadataToolImplementsToolInterface(t *testing.T) {
	baseTool := NewToolFunc(
		"test",
		"test",
		nil,
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			return core.NewResult(true, nil), nil
		},
	)
	metadata := core.ToolMetadata{Version: "1.0.0"}
	tool := WithMetadata(baseTool, metadata)

	// This should compile if metadataTool implements Tool interface
	var _ = tool
}

// TestBaseToolEdgeCases tests edge cases for BaseTool.
func TestBaseToolEdgeCases(t *testing.T) {
	// Test with very long name
	longName := string(make([]byte, 1000))
	for i := range longName {
		longName = longName[:i] + "a" + longName[i+1:]
	}

	tool := NewBaseTool(longName, "Tool with long name", nil)
	if tool.Name() != longName {
		t.Error("Name() should handle long names")
	}

	// Test with special characters in name
	specialName := "tool-with_special.chars@123"
	tool = NewBaseTool(specialName, "Tool with special name", nil)
	if tool.Name() != specialName {
		t.Error("Name() should handle special characters")
	}

	// Test with unicode in description
	unicodeDesc := "这是一个测试工具 - This is a test tool"
	tool = NewBaseTool("unicode_tool", unicodeDesc, nil)
	if tool.Description() != unicodeDesc {
		t.Error("Description() should handle unicode characters")
	}
}

// TestToolFuncWithComplexParams tests ToolFunc with complex parameters.
func TestToolFuncWithComplexParams(t *testing.T) {
	tool := NewToolFunc(
		"complex_tool",
		"A tool with complex parameters",
		&core.ParameterSchema{
			Type: "object",
			Properties: map[string]*core.Parameter{
				"string_param": {
					Type:        "string",
					Description: "A string parameter",
				},
				"number_param": {
					Type:        "number",
					Description: "A number parameter",
				},
				"bool_param": {
					Type:        "boolean",
					Description: "A boolean parameter",
				},
				"array_param": {
					Type:        "array",
					Description: "An array parameter",
				},
			},
			Required: []string{"string_param"},
		},
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			// Validate required parameter
			if _, ok := params["string_param"]; !ok {
				return core.NewErrorResult("string_param is required"), nil
			}

			return core.NewResult(true, params), nil
		},
	)

	ctx := context.Background()

	// Test with all parameters
	params := map[string]interface{}{
		"string_param": "test",
		"number_param": 42.0,
		"bool_param":   true,
		"array_param":  []interface{}{"a", "b", "c"},
	}

	result, err := tool.Execute(ctx, params)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Error("result should be successful")
	}

	// Test with missing required parameter
	params = map[string]interface{}{
		"number_param": 42.0,
	}

	result, err = tool.Execute(ctx, params)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Success {
		t.Error("result should fail for missing required parameter")
	}
}
