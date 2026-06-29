// Package discovery provides the public API for service discovery.
//
// External projects use this package to discover MCP servers and other services.
// The core engine lives in internal/discovery; this package exposes only the
// interfaces and constructors needed by consumers.
//
// Usage:
//
//	import "github.com/Timwood0x10/ares/api/discovery"
//
//	engine := discovery.NewEngine(discovery.DefaultProviders(""), nil)
//	engine.Start(ctx, 5*time.Minute)
//	engine.OnEvent(func(evt discovery.Event) { ... })
//	services, _ := engine.List(ctx)
package discovery

import (
	"context"
	"time"

	internal "github.com/Timwood0x10/ares/internal/discovery"
	"github.com/Timwood0x10/ares/internal/discovery/providers"
)

// Re-export types for external consumers.
type (
	ServiceType       = internal.ServiceType
	Confidence        = internal.Confidence
	ServiceIdentity   = internal.ServiceIdentity
	DiscoveryRecord   = internal.DiscoveryRecord
	DiscoveredService = internal.DiscoveredService
	HealthStatus      = internal.HealthStatus
	EventType         = internal.EventType
	Event             = internal.Event
)

// Re-export constants.
const (
	ServiceTypeMCP    = internal.ServiceTypeMCP
	ServiceTypeHTTP   = internal.ServiceTypeHTTP
	ServiceTypeGRPC   = internal.ServiceTypeGRPC
	ServiceTypeCLI    = internal.ServiceTypeCLI
	ServiceTypeDocker = internal.ServiceTypeDocker

	ConfidenceLow    = internal.ConfidenceLow
	ConfidenceMedium = internal.ConfidenceMedium
	ConfidenceHigh   = internal.ConfidenceHigh
	ConfidenceMax    = internal.ConfidenceMax

	EventServiceAdded      = internal.EventServiceAdded
	EventServiceRemoved    = internal.EventServiceRemoved
	EventServiceUpdated    = internal.EventServiceUpdated
	EventHealthChanged     = internal.EventHealthChanged
	EventDiscoveryComplete = internal.EventDiscoveryComplete
)

// Engine is the public handle for the discovery engine.
type Engine struct {
	inner *internal.Engine
}

// NewEngine creates a discovery engine with default providers.
// projectDir is used for project-level config scanning (can be "").
// health can be nil to skip health checks.
func NewEngine(projectDir string, health internal.HealthChecker) *Engine {
	store := internal.NewMemoryStore()
	inner := internal.NewEngine(store, health)

	// Register default providers.
	inner.AddProvider(providers.NewARESProvider())
	inner.AddProvider(providers.NewClaudeProvider(projectDir))
	inner.AddProvider(providers.NewCursorProvider())
	inner.AddProvider(providers.NewVSCodeProvider(projectDir))
	inner.AddProvider(providers.NewBinaryProbeProvider())

	return &Engine{inner: inner}
}

// Start begins periodic discovery.
func (e *Engine) Start(ctx context.Context, interval time.Duration) {
	e.inner.StartAutoDiscovery(ctx, interval)
}

// DiscoverNow runs an immediate discovery cycle.
func (e *Engine) DiscoverNow(ctx context.Context) error {
	return e.inner.DiscoverNow(ctx)
}

// List returns all known services.
func (e *Engine) List(ctx context.Context) ([]*DiscoveredService, error) {
	return e.inner.List(ctx)
}

// Get returns a service by ID.
func (e *Engine) Get(ctx context.Context, id string) (*DiscoveredService, error) {
	return e.inner.Get(ctx, id)
}

// OnEvent registers a callback for discovery events.
func (e *Engine) OnEvent(fn func(Event)) {
	e.inner.AddHandler(internal.EventHandlerFunc(fn))
}
