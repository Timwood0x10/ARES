package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Engine orchestrates the discovery lifecycle:
//
//	Providers → Merge → Health → Events
type Engine struct {
	store     ServiceStore
	health    HealthChecker
	providers []DiscoveryProvider
	handlers  []EventHandler
	mu        sync.RWMutex
}

// NewEngine creates a discovery engine.
func NewEngine(store ServiceStore, health HealthChecker) *Engine {
	return &Engine{
		store:  store,
		health: health,
	}
}

// AddProvider registers a discovery provider.
func (e *Engine) AddProvider(p DiscoveryProvider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.providers = append(e.providers, p)
}

// AddHandler registers an event handler.
func (e *Engine) AddHandler(h EventHandler) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers = append(e.handlers, h)
}

// DiscoverNow runs a full discovery cycle.
func (e *Engine) DiscoverNow(ctx context.Context) error {
	e.mu.RLock()
	providers := make([]DiscoveryProvider, len(e.providers))
	copy(providers, e.providers)
	e.mu.RUnlock()

	// Phase 1: Collect records from all providers concurrently.
	type providerResult struct {
		records []DiscoveryRecord
		name    string
	}
	results := make([]providerResult, len(providers))
	g, gctx := errgroup.WithContext(ctx)
	for i, p := range providers {
		idx := i
		prov := p
		g.Go(func() error {
			records, err := prov.Discover(gctx)
			if err != nil {
				slog.Warn("discovery: provider failed",
					"provider", prov.Name(),
					"error", err,
				)
				return nil // Don't fail the whole group.
			}
			for j := range records {
				records[j].LastSeen = time.Now()
			}
			results[idx] = providerResult{records: records, name: prov.Name()}
			return nil
		})
	}
	_ = g.Wait() // Errors are logged, not propagated.

	var allRecords []DiscoveryRecord
	for _, r := range results {
		allRecords = append(allRecords, r.records...)
	}

	// Phase 2: Merge records into services.
	newServices := mergeRecords(allRecords)

	// Phase 3: Compare with existing store.
	existingList, err := e.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list existing services: %w", err)
	}
	existing := make(map[string]*DiscoveredService, len(existingList))
	for _, svc := range existingList {
		existing[svc.Identity.ID] = svc
	}

	added, updated, removed := diffServices(existing, newServices)

	// Phase 4: Persist changes and emit events.
	for _, id := range added {
		svc := newServices[id]
		if err := e.store.Save(ctx, svc); err != nil {
			slog.Warn("discovery: save failed", "id", id, "error", err)
			continue
		}
		e.emit(Event{
			Type:      EventServiceAdded,
			ServiceID: id,
			Service:   svc,
			Source:    svc.BestSource,
			Timestamp: time.Now(),
		})
	}

	for _, id := range updated {
		svc := newServices[id]
		if err := e.store.Save(ctx, svc); err != nil {
			slog.Warn("discovery: save failed", "id", id, "error", err)
			continue
		}
		e.emit(Event{
			Type:      EventServiceUpdated,
			ServiceID: id,
			Service:   svc,
			Source:    svc.BestSource,
			Timestamp: time.Now(),
		})
	}

	for _, id := range removed {
		if err := e.store.Delete(ctx, id); err != nil {
			slog.Warn("discovery: delete failed", "id", id, "error", err)
			continue
		}
		e.emit(Event{
			Type:      EventServiceRemoved,
			ServiceID: id,
			Message:   "service no longer found",
			Timestamp: time.Now(),
		})
	}

	e.emit(Event{
		Type:      EventDiscoveryComplete,
		Message:   fmt.Sprintf("added=%d updated=%d removed=%d", len(added), len(updated), len(removed)),
		Timestamp: time.Now(),
	})

	return nil
}

// CheckHealth runs health checks on all known services.
func (e *Engine) CheckHealth(ctx context.Context) error {
	if e.health == nil {
		return nil
	}

	services, err := e.store.List(ctx)
	if err != nil {
		return fmt.Errorf("list services: %w", err)
	}

	for _, svc := range services {
		status, err := e.health.CheckHealth(ctx, svc)
		if err != nil {
			slog.Warn("discovery: health check error",
				"id", svc.Identity.ID,
				"error", err,
			)
			continue
		}

		oldHealthy := svc.Healthy
		svc.Healthy = status.Healthy
		svc.HealthMsg = status.Message
		now := time.Now()
		svc.CheckedAt = &now

		if err := e.store.Save(ctx, svc); err != nil {
			slog.Warn("discovery: save health failed", "id", svc.Identity.ID, "error", err)
			continue
		}

		if oldHealthy != status.Healthy {
			e.emit(Event{
				Type:      EventHealthChanged,
				ServiceID: svc.Identity.ID,
				Service:   svc,
				Message:   status.Message,
				Timestamp: time.Now(),
			})
		}
	}

	return nil
}

// StartAutoDiscovery starts periodic discovery and health checks.
func (e *Engine) StartAutoDiscovery(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	go func() {
		if err := e.DiscoverNow(ctx); err != nil {
			slog.Warn("discovery: initial cycle failed", "error", err)
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := e.DiscoverNow(ctx); err != nil {
					slog.Warn("discovery: cycle failed", "error", err)
				}
				if err := e.CheckHealth(ctx); err != nil {
					slog.Warn("discovery: health check failed", "error", err)
				}
			}
		}
	}()
}

// List returns all known services.
func (e *Engine) List(ctx context.Context) ([]*DiscoveredService, error) {
	return e.store.List(ctx)
}

// Get returns a service by ID.
func (e *Engine) Get(ctx context.Context, id string) (*DiscoveredService, error) {
	return e.store.Get(ctx, id)
}

// RegisterRequest is the input for passive service registration.
type RegisterRequest struct {
	Name       string            `json:"name"`
	Endpoint   string            `json:"endpoint"`
	Args       []string          `json:"args,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	Confidence Confidence        `json:"confidence,omitempty"`
}

// Register passively registers a service. Emits EventServiceAdded.
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

	svc := &DiscoveredService{
		Identity: ServiceIdentity{
			ID:       req.Name,
			Name:     req.Name,
			Type:     ServiceTypeMCP,
			Endpoint: req.Endpoint,
			Tags:     req.Tags,
			Metadata: req.Metadata,
		},
		Records: []DiscoveryRecord{
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
		Healthy:    false,
	}

	if err := e.store.Save(ctx, svc); err != nil {
		return err
	}
	e.emit(Event{
		Type:      EventServiceAdded,
		ServiceID: req.Name,
		Service:   svc,
		Source:    "register",
		Timestamp: time.Now(),
	})
	return nil
}

// Unregister removes a service by ID. Emits EventServiceRemoved.
func (e *Engine) Unregister(ctx context.Context, id string) error {
	if err := e.store.Delete(ctx, id); err != nil {
		return err
	}
	e.emit(Event{
		Type:      EventServiceRemoved,
		ServiceID: id,
		Message:   "unregistered",
		Timestamp: time.Now(),
	})
	return nil
}

// UpdateTagsRequest modifies tags on a service.
type UpdateTagsRequest struct {
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
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

	if err := e.store.Save(ctx, svc); err != nil {
		return err
	}
	e.emit(Event{
		Type:      EventServiceUpdated,
		ServiceID: id,
		Service:   svc,
		Timestamp: time.Now(),
	})
	return nil
}

// emit sends an event to all registered handlers.
func (e *Engine) emit(evt Event) {
	e.mu.RLock()
	handlers := make([]EventHandler, len(e.handlers))
	copy(handlers, e.handlers)
	e.mu.RUnlock()

	for _, h := range handlers {
		h.HandleDiscoveryEvent(evt)
	}
}
