package provider

import (
	"fmt"
	"sort"
	"sync"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// ProviderRegistry manages a collection of GraphProviders, allowing
// SourceDiscovery to select the best providers for a given intent.
type ProviderRegistry struct {
	mu        sync.RWMutex
	providers map[string]GraphProvider
}

// NewProviderRegistry creates an empty provider registry.
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]GraphProvider),
	}
}

// Register adds a provider to the registry. Returns an error if a provider
// with the same name already exists.
func (r *ProviderRegistry) Register(p GraphProvider) error {
	if p == nil {
		return fmt.Errorf("cannot register nil provider")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	name := p.Name()
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("provider %q already registered", name)
	}
	r.providers[name] = p
	return nil
}

// Unregister removes a provider by name. Returns an error if not found.
func (r *ProviderRegistry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[name]; !exists {
		return fmt.Errorf("provider %q not found", name)
	}
	delete(r.providers, name)
	return nil
}

// Get returns a provider by name. Returns nil if not found.
func (r *ProviderRegistry) Get(name string) GraphProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.providers[name]
}

// List returns all registered provider names.
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Select returns providers matching the given intent, sorted by match score
// descending. Only providers with a score above the threshold (default 0.1)
// are returned. An empty list means no provider matches the intent.
func (r *ProviderRegistry) Select(intent knowledge.Intent, threshold float64) []GraphProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if threshold <= 0 {
		threshold = 0.1
	}

	type scored struct {
		p     GraphProvider
		score float64
	}
	var scoredProviders []scored

	for _, p := range r.providers {
		s := p.IntentMatch(intent)
		if s >= threshold {
			scoredProviders = append(scoredProviders, scored{p, s})
		}
	}

	sort.Slice(scoredProviders, func(i, j int) bool {
		return scoredProviders[i].score > scoredProviders[j].score
	})

	result := make([]GraphProvider, len(scoredProviders))
	for i, sp := range scoredProviders {
		result[i] = sp.p
	}
	return result
}
