package core

import (
	"context"
	"testing"
)

// MockToolFactory is a mock factory for testing.
type MockToolFactory struct {
	name        string
	description string
	createErr   error
	validateErr error
}

func (f *MockToolFactory) Name() string {
	return f.name
}

func (f *MockToolFactory) Description() string {
	return f.description
}

func (f *MockToolFactory) Create(config map[string]interface{}) (Tool, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &MockToolForFactory{name: f.name}, nil
}

func (f *MockToolFactory) ValidateConfig(config map[string]interface{}) error {
	return f.validateErr
}

// MockToolForFactory is a mock tool for testing.
type MockToolForFactory struct {
	name string
}

func (t *MockToolForFactory) Name() string                 { return t.name }
func (t *MockToolForFactory) Description() string          { return "mock tool" }
func (t *MockToolForFactory) Category() ToolCategory       { return CategoryCore }
func (t *MockToolForFactory) Capabilities() []Capability   { return nil }
func (t *MockToolForFactory) Parameters() *ParameterSchema { return nil }
func (t *MockToolForFactory) Execute(ctx context.Context, params map[string]interface{}) (Result, error) {
	return NewResult(true, "mock"), nil
}

func TestPluginRegistry_RegisterFactory(t *testing.T) {
	registry := NewPluginRegistry()

	tests := []struct {
		name      string
		factory   ToolFactory
		expectErr error
	}{
		{
			name:    "valid factory",
			factory: &MockToolFactory{name: "test", description: "test factory"},
		},
		{
			name:      "nil factory",
			factory:   nil,
			expectErr: ErrNilFactory,
		},
		{
			name:      "empty name",
			factory:   &MockToolFactory{name: "", description: "test"},
			expectErr: ErrEmptyFactoryName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.RegisterFactory(tt.factory)

			if tt.expectErr != nil {
				if err != tt.expectErr {
					t.Errorf("expected error %v, got %v", tt.expectErr, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}

	// Test duplicate registration
	err := registry.RegisterFactory(&MockToolFactory{name: "test", description: "duplicate"})
	if err != ErrFactoryAlreadyExists {
		t.Errorf("expected ErrFactoryAlreadyExists, got %v", err)
	}
}

func TestPluginRegistry_LoadPlugins(t *testing.T) {
	registry := NewPluginRegistry()

	// Register a factory
	factory := &MockToolFactory{name: "test-factory", description: "test"}
	if err := registry.RegisterFactory(factory); err != nil {
		t.Fatalf("failed to register factory: %v", err)
	}

	tests := []struct {
		name      string
		configs   []PluginConfig
		expectErr bool
	}{
		{
			name: "enabled plugin",
			configs: []PluginConfig{
				{Name: "tool1", Factory: "test-factory", Enabled: true},
			},
		},
		{
			name: "disabled plugin",
			configs: []PluginConfig{
				{Name: "tool2", Factory: "test-factory", Enabled: false},
			},
		},
		{
			name: "non-existent factory",
			configs: []PluginConfig{
				{Name: "tool3", Factory: "non-existent", Enabled: true},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := registry.LoadPlugins(tt.configs)

			if tt.expectErr {
				if err == nil {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestPluginRegistry_GetTool(t *testing.T) {
	registry := NewPluginRegistry()
	factory := &MockToolFactory{name: "test", description: "test"}
	_ = registry.RegisterFactory(factory)

	configs := []PluginConfig{
		{Name: "my-tool", Factory: "test", Enabled: true},
	}

	if err := registry.LoadPlugins(configs); err != nil {
		t.Fatalf("failed to load plugins: %v", err)
	}

	// Test getting existing tool
	tool, exists := registry.GetTool("my-tool")
	if !exists {
		t.Error("expected tool to exist")
	}
	if tool == nil {
		t.Error("expected non-nil tool")
	}

	// Test getting non-existent tool
	_, exists = registry.GetTool("non-existent")
	if exists {
		t.Error("expected tool to not exist")
	}
}

func TestPluginRegistry_ListPlugins(t *testing.T) {
	registry := NewPluginRegistry()
	factory := &MockToolFactory{name: "test", description: "test"}
	_ = registry.RegisterFactory(factory)

	configs := []PluginConfig{
		{Name: "tool1", Factory: "test", Enabled: true},
		{Name: "tool2", Factory: "test", Enabled: false}, // Should not be listed
	}

	if err := registry.LoadPlugins(configs); err != nil {
		t.Fatalf("failed to load plugins: %v", err)
	}

	plugins := registry.ListPlugins()
	if len(plugins) != 1 {
		t.Errorf("expected 1 plugin, got %d", len(plugins))
	}
}

func TestPluginRegistry_ListFactories(t *testing.T) {
	registry := NewPluginRegistry()

	factories := []ToolFactory{
		&MockToolFactory{name: "factory1", description: "test"},
		&MockToolFactory{name: "factory2", description: "test"},
	}

	for _, f := range factories {
		if err := registry.RegisterFactory(f); err != nil {
			t.Fatalf("failed to register factory: %v", err)
		}
	}

	list := registry.ListFactories()
	if len(list) != 2 {
		t.Errorf("expected 2 factories, got %d", len(list))
	}
}

func TestPluginConfig(t *testing.T) {
	config := PluginConfig{
		Name:    "test-tool",
		Factory: "test-factory",
		Enabled: true,
		Config:  map[string]interface{}{"key": "value"},
	}

	if config.Name != "test-tool" {
		t.Errorf("name mismatch")
	}

	if !config.Enabled {
		t.Error("expected enabled")
	}
}
