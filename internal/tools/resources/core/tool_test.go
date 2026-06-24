package core

import (
	"context"
	"testing"
)

// TestToolCategory tests ToolCategory constants.
func TestToolCategory(t *testing.T) {
	tests := []struct {
		name string
		cat  ToolCategory
		want string
	}{
		{
			name: "system category",
			cat:  CategorySystem,
			want: "system",
		},
		{
			name: "core category",
			cat:  CategoryCore,
			want: "core",
		},
		{
			name: "data category",
			cat:  CategoryData,
			want: "data",
		},
		{
			name: "knowledge category",
			cat:  CategoryKnowledge,
			want: "knowledge",
		},
		{
			name: "memory category",
			cat:  CategoryMemory,
			want: "memory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.cat) != tt.want {
				t.Errorf("got %q, want %q", tt.cat, tt.want)
			}
		})
	}
}

// TestToolCategoryUniqueness ensures all categories are unique.
func TestToolCategoryUniqueness(t *testing.T) {
	categories := map[ToolCategory]bool{
		CategorySystem:    true,
		CategoryCore:      true,
		CategoryData:      true,
		CategoryKnowledge: true,
		CategoryMemory:    true,
	}

	if len(categories) != 5 {
		t.Errorf("expected 6 unique categories, got %d", len(categories))
	}
}

// TestParameter tests Parameter structure.
func TestParameter(t *testing.T) {
	tests := []struct {
		name string
		p    Parameter
	}{
		{
			name: "fully populated parameter",
			p: Parameter{
				Type:        "string",
				Description: "A string parameter",
				Default:     "default_value",
				Enum:        []interface{}{"option1", "option2"},
				Min:         float64Ptr(0.0),
				Max:         float64Ptr(100.0),
			},
		},
		{
			name: "minimal parameter",
			p: Parameter{
				Type:        "integer",
				Description: "An integer parameter",
			},
		},
		{
			name: "parameter with only default",
			p: Parameter{
				Type:        "boolean",
				Default:     true,
				Description: "A boolean parameter with default value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.p.Type == "" {
				t.Error("Type should not be empty")
			}
			if tt.p.Description == "" {
				t.Error("Description should not be empty")
			}
		})
	}
}

// TestParameterSchema tests ParameterSchema structure.
func TestParameterSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema *ParameterSchema
	}{
		{
			name: "fully populated schema",
			schema: &ParameterSchema{
				Type: "object",
				Properties: map[string]*Parameter{
					"param1": {
						Type:        "string",
						Description: "First parameter",
					},
					"param2": {
						Type:        "integer",
						Description: "Second parameter",
					},
				},
				Required: []string{"param1"},
			},
		},
		{
			name: "minimal schema",
			schema: &ParameterSchema{
				Type:       "object",
				Properties: map[string]*Parameter{},
				Required:   []string{},
			},
		},
		{
			name:   "nil schema",
			schema: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.schema == nil {
				return
			}

			if tt.schema.GetType() != tt.schema.Type {
				t.Error("GetType() should return schema.Type")
			}

			props := tt.schema.GetProperties()
			if props != nil && tt.schema.Properties != nil {
				if len(props) != len(tt.schema.Properties) {
					t.Error("GetProperties() should return schema.Properties")
				}
			}

			required := tt.schema.GetRequired()
			if required != nil && tt.schema.Required != nil {
				if len(required) != len(tt.schema.Required) {
					t.Error("GetRequired() should return schema.Required")
				}
			}
		})
	}
}

// TestParameterSchemaNilSafety tests nil safety of ParameterSchema methods.
func TestParameterSchemaNilSafety(t *testing.T) {
	var schema *ParameterSchema

	if schema.GetType() != "" {
		t.Error("GetType() should return empty string for nil schema")
	}

	if schema.GetProperties() != nil {
		t.Error("GetProperties() should return nil for nil schema")
	}

	if schema.GetRequired() != nil {
		t.Error("GetRequired() should return nil for nil schema")
	}
}

// TestToolMetadata tests ToolMetadata structure.
func TestToolMetadata(t *testing.T) {
	tests := []struct {
		name string
		meta ToolMetadata
	}{
		{
			name: "fully populated metadata",
			meta: ToolMetadata{
				Version:     "1.0.0",
				Author:      "test-author",
				Tags:        []string{"tag1", "tag2"},
				Examples:    []string{"example1", "example2"},
				Deprecated:  true,
				Deprecation: "Use new_tool instead",
			},
		},
		{
			name: "minimal metadata",
			meta: ToolMetadata{
				Version: "1.0.0",
			},
		},
		{
			name: "metadata with empty arrays",
			meta: ToolMetadata{
				Version:  "1.0.0",
				Tags:     []string{},
				Examples: []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.meta.Version == "" {
				t.Error("Version should not be empty")
			}
		})
	}
}

// TestToolSchema tests ToolSchema structure.
func TestToolSchema(t *testing.T) {
	tests := []struct {
		name   string
		schema ToolSchema
	}{
		{
			name: "fully populated schema",
			schema: ToolSchema{
				Name:        "test_tool",
				Description: "A test tool",
				Category:    CategoryCore,
				Parameters: &ParameterSchema{
					Type: "object",
					Properties: map[string]*Parameter{
						"param": {
							Type:        "string",
							Description: "A parameter",
						},
					},
					Required: []string{"param"},
				},
			},
		},
		{
			name: "minimal schema",
			schema: ToolSchema{
				Name:        "simple_tool",
				Description: "A simple tool",
				Category:    CategorySystem,
			},
		},
		{
			name: "schema with nil parameters",
			schema: ToolSchema{
				Name:        "nil_params_tool",
				Description: "Tool with nil parameters",
				Category:    CategoryData,
				Parameters:  nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.schema.Name == "" {
				t.Error("Name should not be empty")
			}
			if tt.schema.Description == "" {
				t.Error("Description should not be empty")
			}
			if tt.schema.Category == "" {
				t.Error("Category should not be empty")
			}
		})
	}
}

// TestMockTool tests a mock implementation of the Tool interface.
func TestMockTool(t *testing.T) {
	mock := &MockTool{
		name:        "mock_tool",
		description: "A mock tool for testing",
		category:    CategoryCore,
	}

	ctx := context.Background()
	result, err := mock.Execute(ctx, map[string]interface{}{
		"param": "value",
	})

	if err != nil {
		t.Errorf("Execute() error = %v", err)
	}

	if !result.Success {
		t.Error("Execute() should return a successful result")
	}

	if mock.Name() != "mock_tool" {
		t.Errorf("Name() = %q, want %q", mock.Name(), "mock_tool")
	}

	if mock.Description() != "A mock tool for testing" {
		t.Errorf("Description() = %q, want %q", mock.Description(), "A mock tool for testing")
	}

	if mock.Category() != CategoryCore {
		t.Errorf("Category() = %q, want %q", mock.Category(), CategoryCore)
	}

	caps := mock.Capabilities()
	if caps == nil {
		t.Error("Capabilities() should not return nil")
	}

	params := mock.Parameters()
	if params == nil {
		t.Error("Parameters() should not return nil")
	}
}

// MockTool is a mock implementation of the Tool interface for testing.
type MockTool struct {
	name        string
	description string
	category    ToolCategory
}

func (m *MockTool) Name() string {
	return m.name
}

func (m *MockTool) Description() string {
	return m.description
}

func (m *MockTool) Category() ToolCategory {
	return m.category
}

func (m *MockTool) Capabilities() []Capability {
	return []Capability{
		CapabilityMath,
	}
}

func (m *MockTool) Execute(ctx context.Context, params map[string]interface{}) (Result, error) {
	return Result{
		Success: true,
		Data: map[string]interface{}{
			"executed": true,
			"params":   params,
		},
	}, nil
}

func (m *MockTool) Parameters() *ParameterSchema {
	return &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"param": {
				Type:        "string",
				Description: "A parameter",
			},
		},
		Required: []string{"param"},
	}
}

// float64Ptr returns a pointer to a float64 value.
func float64Ptr(f float64) *float64 {
	return &f
}

// TestParameterEdgeCases tests edge cases for Parameter.
func TestParameterEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		p    Parameter
	}{
		{
			name: "min greater than max",
			p: Parameter{
				Type:        "number",
				Description: "Invalid range parameter",
				Min:         float64Ptr(100.0),
				Max:         float64Ptr(50.0),
			},
		},
		{
			name: "empty enum",
			p: Parameter{
				Type:        "string",
				Description: "Empty enum parameter",
				Enum:        []interface{}{},
			},
		},
		{
			name: "nil min and max",
			p: Parameter{
				Type:        "number",
				Description: "Parameter without bounds",
				Min:         nil,
				Max:         nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.p.Min != nil && tt.p.Max != nil && *tt.p.Min > *tt.p.Max {
				t.Logf("Warning: Min (%f) is greater than Max (%f)", *tt.p.Min, *tt.p.Max)
			}
		})
	}
}
