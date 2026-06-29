// Package discovery provides the public API for service discovery.
//
// Two modes:
//   - Active discovery: engine scans providers (config files, binary probe, HTTP).
//   - Passive registration: external services register themselves via Register().
//
// Storage is pluggable via ServiceStore interface.
//
// Usage:
//
//	import "github.com/Timwood0x10/ares/api/discovery"
//
//	// Create engine with custom store (e.g. SQLite).
//	store := NewSQLiteStore("discovery.db")
//	engine := discovery.NewEngine(discovery.EngineConfig{
//	    ProjectDir: ".",
//	    Store:      store,
//	})
//
//	// Passive registration — services register themselves.
//	engine.Register(ctx, discovery.RegisterRequest{
//	    Name:     "my-mcp",
//	    Endpoint: "/usr/bin/my-mcp",
//	    Tags:     []string{"capability:search", "domain:code"},
//	})
//
//	// Active discovery — scan providers.
//	engine.DiscoverNow(ctx)
//
//	// Events → DB.
//	engine.OnEvent(func(evt discovery.Event) { store.Save(evt) })
package discovery

import (
	"context"
	"fmt"
	"time"

	internal "github.com/Timwood0x10/ares/internal/discovery"
	"github.com/Timwood0x10/ares/internal/discovery/providers"
)

// Re-export types.
type (
	ServiceType       = internal.ServiceType
	Confidence        = internal.Confidence
	ServiceIdentity   = internal.ServiceIdentity
	DiscoveryRecord   = internal.DiscoveryRecord
	DiscoveredService = internal.DiscoveredService
	HealthStatus      = internal.HealthStatus
	EventType         = internal.EventType
	Event             = internal.Event
	ServiceStore      = internal.ServiceStore
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

// Re-export constructors for built-in stores.
var NewMemoryStore = internal.NewMemoryStore

// EngineConfig configures the discovery engine.
type EngineConfig struct {
	// ProjectDir for project-level config scanning.
	ProjectDir string
	// Store for persisting services. Defaults to MemoryStore if nil.
	Store ServiceStore
	// Health checker. nil to skip health checks.
	Health internal.HealthChecker
}

// Engine is the public handle for the discovery engine.
type Engine struct {
	inner *internal.Engine
	store ServiceStore
}

// NewEngine creates a discovery engine.
func NewEngine(cfg EngineConfig) *Engine {
	store := cfg.Store
	if store == nil {
		store = internal.NewMemoryStore()
	}

	inner := internal.NewEngine(store, cfg.Health)

	// Register default providers.
	inner.AddProvider(providers.NewARESProvider())
	inner.AddProvider(providers.NewClaudeProvider(cfg.ProjectDir))
	inner.AddProvider(providers.NewCursorProvider())
	inner.AddProvider(providers.NewVSCodeProvider(cfg.ProjectDir))
	inner.AddProvider(providers.NewBinaryProbeProvider())

	return &Engine{inner: inner, store: store}
}

// ── Active Discovery ─────────────────────────────────────

// Start begins periodic discovery at the given interval.
func (e *Engine) Start(ctx context.Context, interval time.Duration) {
	e.inner.StartAutoDiscovery(ctx, interval)
}

// DiscoverNow runs an immediate discovery cycle.
func (e *Engine) DiscoverNow(ctx context.Context) error {
	return e.inner.DiscoverNow(ctx)
}

// CheckHealth runs health checks on all known services.
func (e *Engine) CheckHealth(ctx context.Context) error {
	return e.inner.CheckHealth(ctx)
}

// ── Passive Registration ─────────────────────────────────

// RegisterRequest is the input for passive service registration.
type RegisterRequest struct {
	Name       string            `json:"name"`
	Endpoint   string            `json:"endpoint"`
	Args       []string          `json:"args,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Confidence Confidence        `json:"confidence,omitempty"` // Default: ConfidenceMax (100).
}

// Register passively registers a service. The service is immediately available
// without going through discovery providers. Emits EventServiceAdded.
func (e *Engine) Register(ctx context.Context, req RegisterRequest) error {
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if req.Endpoint == "" {
		return fmt.Errorf("endpoint is required")
	}
	if req.Confidence == 0 {
		req.Confidence = ConfidenceMax
	}

	svc := &internal.DiscoveredService{
		Identity: internal.ServiceIdentity{
			ID:       req.Name,
			Name:     req.Name,
			Type:     internal.ServiceTypeMCP,
			Endpoint: req.Endpoint,
			Tags:     req.Tags,
			Metadata: req.Metadata,
		},
		Records: []internal.DiscoveryRecord{
			{
				Source:     "register",
				Confidence: req.Confidence,
				Endpoint:   req.Endpoint,
				Args:       req.Args,
				Tags:       req.Tags,
				Metadata:   req.Metadata,
				LastSeen:   time.Now(),
			},
		},
		BestSource: "register",
		Healthy:    true,
	}

	return e.store.Save(ctx, svc)
}

// Unregister removes a service by ID. Emits EventServiceRemoved.
func (e *Engine) Unregister(ctx context.Context, id string) error {
	return e.store.Delete(ctx, id)
}

// ── Tag Management ───────────────────────────────────────

// UpdateTagsRequest modifies tags on a service.
type UpdateTagsRequest struct {
	Add    []string `json:"add,omitempty"`    // Tags to add.
	Remove []string `json:"remove,omitempty"` // Tags to remove.
}

// UpdateTags adds or removes tags on a service. Emits EventServiceUpdated.
func (e *Engine) UpdateTags(ctx context.Context, id string, req UpdateTagsRequest) error {
	svc, err := e.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if svc == nil {
		return fmt.Errorf("service not found: %s", id)
	}

	tagSet := make(map[string]bool)
	for _, t := range svc.Identity.Tags {
		tagSet[t] = true
	}
	for _, t := range req.Add {
		tagSet[t] = true
	}
	for _, t := range req.Remove {
		delete(tagSet, t)
	}

	newTags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		newTags = append(newTags, t)
	}
	svc.Identity.Tags = newTags

	return e.store.Save(ctx, svc)
}

// ── Query ────────────────────────────────────────────────

// List returns all known services.
func (e *Engine) List(ctx context.Context) ([]*DiscoveredService, error) {
	return e.store.List(ctx)
}

// Get returns a service by ID.
func (e *Engine) Get(ctx context.Context, id string) (*DiscoveredService, error) {
	return e.store.Get(ctx, id)
}

// ── Events ───────────────────────────────────────────────

// OnEvent registers a callback for discovery events.
func (e *Engine) OnEvent(fn func(Event)) {
	e.inner.AddHandler(internal.EventHandlerFunc(fn))
}

// ── Provider Management ──────────────────────────────────

// AddProvider registers a custom discovery provider.
func (e *Engine) AddProvider(p internal.DiscoveryProvider) {
	e.inner.AddProvider(p)
}
