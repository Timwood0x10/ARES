package output

import (
	"errors"
	"fmt"
)

// Provider types.
const (
	ProviderOpenAI     = "openai"
	ProviderOllama     = "ollama"
	ProviderOpenRouter = "openrouter"
)

// Factory creates LLM adapters.
type Factory struct {
	adapters map[string]func(*Config) LLMAdapter
}

// NewFactory creates a new Factory.
func NewFactory() *Factory {
	f := &Factory{
		adapters: make(map[string]func(*Config) LLMAdapter),
	}

	f.register(ProviderOpenAI, func(cfg *Config) LLMAdapter {
		return NewOpenAIAdapter(cfg)
	})

	f.register(ProviderOllama, func(cfg *Config) LLMAdapter {
		return NewOllamaAdapter(cfg)
	})

	f.register(ProviderOpenRouter, func(cfg *Config) LLMAdapter {
		return NewOpenRouterAdapter(cfg)
	})

	return f
}

// register registers an adapter factory.
func (f *Factory) register(provider string, factory func(*Config) LLMAdapter) {
	f.adapters[provider] = factory
}

// Create creates an LLM adapter by provider name.
func (f *Factory) Create(provider string, config *Config) (LLMAdapter, error) {
	factory, exists := f.adapters[provider]
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProvider, provider)
	}

	if config == nil {
		config = DefaultConfig()
	}

	return factory(config), nil
}

// ListProviders returns list of supported providers.
func (f *Factory) ListProviders() []string {
	providers := make([]string, 0, len(f.adapters))
	for p := range f.adapters {
		providers = append(providers, p)
	}
	return providers
}

// Factory errors.
var (
	ErrUnsupportedProvider = errors.New("unsupported provider")
)

// Global default factory.
var defaultFactory = NewFactory()

// CreateAdapter creates an adapter using the default factory.
func CreateAdapter(provider string, config *Config) (LLMAdapter, error) {
	return defaultFactory.Create(provider, config)
}

// RegisterProvider registers a custom provider with the default factory.
func RegisterProvider(provider string, factory func(*Config) LLMAdapter) {
	defaultFactory.register(provider, factory)
}

// ListSupportedProviders returns list of supported providers.
func ListSupportedProviders() []string {
	return defaultFactory.ListProviders()
}
