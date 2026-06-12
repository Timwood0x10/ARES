package core

import (
	"context"
	"errors"
	"sync"
)

// ToolFactory defines the interface for creating tool instances.
// Factories are used to register and instantiate tools dynamically.
type ToolFactory interface {
	// Name returns the unique factory identifier.
	// This is used as the key in the factory registry.
	Name() string

	// Description returns a human-readable description of the factory.
	Description() string

	// Create instantiates a new tool with the given configuration.
	// The config map contains tool-specific settings from YAML or other sources.
	Create(config map[string]interface{}) (Tool, error)

	// ValidateConfig validates the configuration before tool creation.
	// Returns an error if the configuration is invalid.
	ValidateConfig(config map[string]interface{}) error
}

// PluginConfig holds configuration for a plugin tool.
type PluginConfig struct {
	// Name is the unique plugin identifier.
	Name string `yaml:"name" json:"name"`
	// Factory is the name of the factory to use for creating this tool.
	Factory string `yaml:"factory" json:"factory"`
	// Enabled controls whether this plugin is loaded.
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Config contains tool-specific configuration.
	Config map[string]interface{} `yaml:"config" json:"config"`
}

// PluginRegistry manages tool factories and plugin instances.
// All methods are safe for concurrent use.
type PluginRegistry struct {
	mu        sync.RWMutex
	factories map[string]ToolFactory
	tools     map[string]Tool
}

// NewPluginRegistry creates a new plugin registry.
func NewPluginRegistry() *PluginRegistry {
	return &PluginRegistry{
		factories: make(map[string]ToolFactory),
		tools:     make(map[string]Tool),
	}
}

// RegisterFactory registers a tool factory.
// Returns an error if a factory with the same name already exists.
func (r *PluginRegistry) RegisterFactory(factory ToolFactory) error {
	if factory == nil {
		return ErrNilFactory
	}

	name := factory.Name()
	if name == "" {
		return ErrEmptyFactoryName
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return ErrFactoryAlreadyExists
	}

	r.factories[name] = factory
	return nil
}

// LoadPlugins instantiates tools from plugin configurations.
// Only enabled plugins are loaded.
func (r *PluginRegistry) LoadPlugins(configs []PluginConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}

		if cfg.Name == "" {
			return ErrEmptyPluginName
		}

		factory, exists := r.factories[cfg.Factory]
		if !exists {
			return ErrFactoryNotFound
		}

		if err := factory.ValidateConfig(cfg.Config); err != nil {
			return err
		}

		tool, err := factory.Create(cfg.Config)
		if err != nil {
			return err
		}

		if _, exists := r.tools[cfg.Name]; exists {
			return ErrPluginAlreadyExists
		}

		r.tools[cfg.Name] = tool
	}
	return nil
}

// GetTool retrieves a tool by name.
func (r *PluginRegistry) GetTool(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, exists := r.tools[name]
	return tool, exists
}

// ListPlugins returns the names of all loaded plugins.
func (r *PluginRegistry) ListPlugins() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

// ListFactories returns the names of all registered factories.
func (r *PluginRegistry) ListFactories() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

// Plugin errors.
var (
	ErrNilFactory           = errors.New("factory is nil")
	ErrEmptyFactoryName     = errors.New("factory name is empty")
	ErrFactoryAlreadyExists = errors.New("factory already exists")
	ErrFactoryNotFound      = errors.New("factory not found")
	ErrEmptyPluginName      = errors.New("plugin name is empty")
	ErrPluginAlreadyExists  = errors.New("plugin already exists")
)

// ToolLifecycle defines optional lifecycle hooks for tools.
type ToolLifecycle interface {
	// Init initializes the tool after creation.
	Init(ctx context.Context) error

	// Stop cleans up resources when the tool is no longer needed.
	Stop(ctx context.Context) error
}
