package core

import (
	"context"
	"fmt"
	"testing"
)

// BenchmarkToolRegistration benchmarks tool registration
func BenchmarkToolRegistration(b *testing.B) {
	tools := make([]Tool, 100)
	for i := range tools {
		tools[i] = &mockTool{
			name:        fmt.Sprintf("tool-%d", i),
			description: "Test tool",
			category:    CategoryCore,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		registry := NewRegistry()
		for _, tool := range tools {
			_ = registry.Register(tool)
		}
	}
}

// BenchmarkToolExecution benchmarks tool execution
func BenchmarkToolExecution(b *testing.B) {
	registry := NewRegistry()
	tool := &mockTool{
		name:        "test-tool",
		description: "Test tool",
		category:    CategoryCore,
		executeFunc: func(ctx context.Context, params map[string]interface{}) (Result, error) {
			return NewResult(true, "success"), nil
		},
	}
	_ = registry.Register(tool)

	ctx := context.Background()
	params := map[string]interface{}{"param1": "value1"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = registry.Execute(ctx, "test-tool", params)
	}
}

// BenchmarkCapabilityDetection benchmarks capability detection
func BenchmarkCapabilityDetection(b *testing.B) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	queries := []string{
		"calculate the sum of two numbers",
		"search for information about golang",
		"remember my preferences",
		"parse the json data",
		"fetch data from the api",
		"what is the current time",
		"read the file content",
		"execute the python script",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, query := range queries {
			_ = engine.Detect(query)
		}
	}
}

// BenchmarkCapabilityMatching benchmarks full capability matching
func BenchmarkCapabilityMatching(b *testing.B) {
	registry := NewRegistry()

	// Register mock tools with different capabilities
	tools := []Tool{
		&mockTool{name: "calculator", capabilities: []Capability{CapabilityMath}},
		&mockTool{name: "knowledge_search", capabilities: []Capability{CapabilityKnowledge}},
		&mockTool{name: "memory_store", capabilities: []Capability{CapabilityMemory}},
		&mockTool{name: "json_parser", capabilities: []Capability{CapabilityText}},
	}

	for _, tool := range tools {
		_ = registry.Register(tool)
	}

	engine := NewCapabilityEngine(registry)

	queries := []string{
		"calculate 10 + 20",
		"what is golang",
		"remember my name is John",
		"parse this json string",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, query := range queries {
			_ = engine.Match(query)
		}
	}
}

// BenchmarkToolFiltering benchmarks tool filtering
func BenchmarkToolFiltering(b *testing.B) {
	registry := NewRegistry()

	// Register 100 tools
	for i := 0; i < 100; i++ {
		tool := &mockTool{
			name:         fmt.Sprintf("tool-%d", i),
			category:     ToolCategory([]string{"system", "core", "data", "knowledge", "memory"}[i%5]),
			capabilities: []Capability{Capability([]string{"math", "knowledge", "memory", "text", "network"}[i%5])},
		}
		_ = registry.Register(tool)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		filter := &ToolFilter{
			Categories: []ToolCategory{CategoryCore, CategoryKnowledge},
		}
		_ = registry.Filter(filter)
	}
}

// BenchmarkResultCreation benchmarks result creation
func BenchmarkResultCreation(b *testing.B) {
	b.Run("Success", func(b *testing.B) {
		data := map[string]interface{}{
			"key1": "value1",
			"key2": 123,
			"key3": []string{"a", "b", "c"},
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = NewResult(true, data)
		}
	})

	b.Run("Error", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = NewErrorResult("test error message")
		}
	})
}

// BenchmarkParameterValidation benchmarks parameter validation
func BenchmarkParameterValidation(b *testing.B) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"string_param": {
				Type:        "string",
				Description: "A string parameter",
			},
			"int_param": {
				Type:        "integer",
				Description: "An integer parameter",
				Min:         ptrFloat64(0),
				Max:         ptrFloat64(1000),
			},
			"required_param": {
				Type:        "string",
				Description: "A required parameter",
			},
		},
		Required: []string{"required_param"},
	}

	params := map[string]interface{}{
		"string_param":   "test value",
		"int_param":      500,
		"required_param": "required value",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = validateParams(schema, params)
	}
}

// BenchmarkConcurrentToolExecution benchmarks concurrent tool execution
func BenchmarkConcurrentToolExecution(b *testing.B) {
	registry := NewRegistry()

	for i := 0; i < 10; i++ {
		tool := &mockTool{
			name:        fmt.Sprintf("tool-%d", i),
			description: "Test tool",
			category:    CategoryCore,
			executeFunc: func(ctx context.Context, params map[string]interface{}) (Result, error) {
				return NewResult(true, "success"), nil
			},
		}
		_ = registry.Register(tool)
	}

	ctx := context.Background()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			toolName := fmt.Sprintf("tool-%d", i%10)
			_, _ = registry.Execute(ctx, toolName, nil)
			i++
		}
	})
}

// Helper types and functions

type mockTool struct {
	name         string
	description  string
	category     ToolCategory
	capabilities []Capability
	executeFunc  func(ctx context.Context, params map[string]interface{}) (Result, error)
}

func (m *mockTool) Name() string                 { return m.name }
func (m *mockTool) Description() string          { return m.description }
func (m *mockTool) Category() ToolCategory       { return m.category }
func (m *mockTool) Capabilities() []Capability   { return m.capabilities }
func (m *mockTool) Parameters() *ParameterSchema { return nil }
func (m *mockTool) Execute(ctx context.Context, params map[string]interface{}) (Result, error) {
	if m.executeFunc != nil {
		return m.executeFunc(ctx, params)
	}
	return NewResult(true, nil), nil
}

func ptrFloat64(v float64) *float64 {
	return &v
}

func validateParams(schema *ParameterSchema, params map[string]interface{}) error {
	// Simplified validation for benchmark
	for _, required := range schema.Required {
		if _, exists := params[required]; !exists {
			return fmt.Errorf("missing required parameter: %s", required)
		}
	}
	return nil
}
