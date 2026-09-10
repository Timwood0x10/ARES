package discovery

import (
	"context"
	"sync"
	"testing"
)

// mockProvider is a test provider that returns fixed records.
type mockProvider struct {
	name    string
	records []DiscoveryRecord
	err     error
}

func (p *mockProvider) Name() string           { return p.name }
func (p *mockProvider) Confidence() Confidence { return ConfidenceHigh }
func (p *mockProvider) Discover(_ context.Context) ([]DiscoveryRecord, error) {
	return p.records, p.err
}

// mockHandler collects events for assertions.
type mockHandler struct {
	mu     sync.Mutex
	events []Event
}

func (h *mockHandler) HandleDiscoveryEvent(evt Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, evt)
}

func (h *mockHandler) Events() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]Event, len(h.events))
	copy(cp, h.events)
	return cp
}

func TestEngine_DiscoverNow_EmitsAdded(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)
	handler := &mockHandler{}
	engine.AddHandler(handler)

	engine.AddProvider(&mockProvider{
		name: "test",
		records: []DiscoveryRecord{
			{Source: "test", Confidence: ConfidenceHigh, Endpoint: "tool-a"},
		},
	})

	ctx := context.Background()
	if err := engine.DiscoverNow(ctx); err != nil {
		t.Fatalf("discover: %v", err)
	}

	// Should have service.added + cycle.complete events.
	events := handler.Events()
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(events))
	}

	found := false
	for _, e := range events {
		if e.Type == EventServiceAdded && e.ServiceID == "tool-a" {
			found = true
		}
	}
	if !found {
		t.Error("expected EventServiceAdded for tool-a")
	}

	// Verify stored.
	svc, _ := store.Get(ctx, "tool-a")
	if svc == nil {
		t.Fatal("expected tool-a in store")
	}
}

func TestEngine_DiscoverNow_EmitsRemoved(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)
	handler := &mockHandler{}
	engine.AddHandler(handler)

	ctx := context.Background()

	// First discovery: tool-a exists.
	engine.AddProvider(&mockProvider{
		name:    "test",
		records: []DiscoveryRecord{{Source: "test", Endpoint: "tool-a"}},
	})
	_ = engine.DiscoverNow(ctx)

	// Second discovery: tool-a gone.
	engine = NewEngine(store, nil)
	engine.AddHandler(handler)
	engine.AddProvider(&mockProvider{name: "test", records: nil})
	_ = engine.DiscoverNow(ctx)

	events := handler.Events()
	found := false
	for _, e := range events {
		if e.Type == EventServiceRemoved && e.ServiceID == "tool-a" {
			found = true
		}
	}
	if !found {
		t.Error("expected EventServiceRemoved for tool-a")
	}
}

func TestEngine_DiscoverNow_EmitsUpdated(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)
	handler := &mockHandler{}
	engine.AddHandler(handler)

	ctx := context.Background()

	// First discovery.
	engine.AddProvider(&mockProvider{
		name:    "test",
		records: []DiscoveryRecord{{Source: "test", Endpoint: "tool-a", Tags: []string{"a"}}},
	})
	_ = engine.DiscoverNow(ctx)

	// Second discovery: tags changed.
	engine = NewEngine(store, nil)
	engine.AddHandler(handler)
	engine.AddProvider(&mockProvider{
		name:    "test",
		records: []DiscoveryRecord{{Source: "test", Endpoint: "tool-a", Tags: []string{"a", "b"}}},
	})
	_ = engine.DiscoverNow(ctx)

	events := handler.Events()
	found := false
	for _, e := range events {
		if e.Type == EventServiceUpdated {
			found = true
		}
	}
	if !found {
		t.Error("expected EventServiceUpdated when tags change")
	}
}

func TestEngine_PassiveRegistration(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)
	handler := &mockHandler{}
	engine.AddHandler(handler)

	ctx := context.Background()

	err := engine.Register(ctx, RegisterRequest{
		Name:     "my-tool",
		Endpoint: "/usr/bin/my-tool",
		Tags:     []string{"capability:search"},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	svc, _ := store.Get(ctx, "my-tool")
	if svc == nil {
		t.Fatal("expected my-tool in store")
	}
	if svc.Identity.Name != "my-tool" {
		t.Errorf("expected name 'my-tool', got %q", svc.Identity.Name)
	}
	if len(svc.Identity.Tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(svc.Identity.Tags))
	}
}

func TestEngine_Unregister(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)

	ctx := context.Background()
	_ = engine.Register(ctx, RegisterRequest{Name: "x", Endpoint: "/x"})
	_ = engine.Unregister(ctx, "x")

	svc, _ := store.Get(ctx, "x")
	if svc != nil {
		t.Error("expected nil after unregister")
	}
}

func TestEngine_UpdateTags(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)

	ctx := context.Background()
	_ = engine.Register(ctx, RegisterRequest{
		Name: "x", Endpoint: "/x", Tags: []string{"a", "b"},
	})

	_ = engine.UpdateTags(ctx, "x", UpdateTagsRequest{
		Add:    []string{"c"},
		Remove: []string{"a"},
	})

	svc, _ := store.Get(ctx, "x")
	tagSet := make(map[string]bool)
	for _, tag := range svc.Identity.Tags {
		tagSet[tag] = true
	}
	if !tagSet["b"] || !tagSet["c"] || tagSet["a"] {
		t.Errorf("expected tags {b, c}, got %v", svc.Identity.Tags)
	}
}

func TestEngine_ParallelProviders(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)

	// Add multiple providers that return different services.
	for i := 0; i < 5; i++ {
		engine.AddProvider(&mockProvider{
			name:    "p" + string(rune('0'+i)),
			records: []DiscoveryRecord{{Source: "test", Endpoint: "tool-" + string(rune('0'+i))}},
		})
	}

	ctx := context.Background()
	if err := engine.DiscoverNow(ctx); err != nil {
		t.Fatalf("discover: %v", err)
	}

	list, _ := store.List(ctx)
	if len(list) != 5 {
		t.Errorf("expected 5 services, got %d", len(list))
	}
}

// TestEngine_DiscoverNowPreservesRegisteredServices is the #51 regression:
// Register() saves a service with a "register"-source record, but the next
// DiscoverNow diffed provider records against the whole store and deleted
// anything the providers did not report — including manually registered
// services. Passive registrations must survive discovery cycles.
func TestEngine_DiscoverNowPreservesRegisteredServices(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)
	handler := &mockHandler{}
	engine.AddHandler(handler)

	ctx := context.Background()

	// Provider discovers one service.
	engine.AddProvider(&mockProvider{
		name: "test",
		records: []DiscoveryRecord{
			{Source: "test", Confidence: ConfidenceHigh, Endpoint: "tool-a"},
		},
	})
	_ = engine.DiscoverNow(ctx)

	// Manually register a second service the provider does NOT know.
	err := engine.Register(ctx, RegisterRequest{
		Name:     "my-registered-tool",
		Endpoint: "/usr/local/bin/my-registered-tool",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// A later discovery cycle with no provider reporting the manual service.
	_ = engine.DiscoverNow(ctx)

	svc, err := store.Get(ctx, "my-registered-tool")
	if err != nil || svc == nil {
		t.Fatal("manually registered service was deleted by DiscoverNow")
	}
	if svc.BestSource != "register" {
		t.Errorf("BestSource = %q, want register", svc.BestSource)
	}

	// And no removal event may be emitted for it.
	for _, e := range handler.Events() {
		if e.Type == EventServiceRemoved && e.ServiceID == "my-registered-tool" {
			t.Error("EventServiceRemoved emitted for a manually registered service")
		}
	}
}

// TestEngine_DiscoverNowStillRemovesDiscoveredServices guards the flip side:
// services that came from providers (not registration) must still be removed
// when providers stop reporting them.
func TestEngine_DiscoverNowStillRemovesDiscoveredServices(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)
	ctx := context.Background()

	engine.AddProvider(&mockProvider{
		name:    "test",
		records: []DiscoveryRecord{{Source: "test", Endpoint: "tool-a"}},
	})
	_ = engine.DiscoverNow(ctx)
	if _, err := store.Get(ctx, "tool-a"); err != nil {
		t.Fatalf("tool-a must be discovered first: %v", err)
	}

	// Provider stops reporting tool-a.
	engine = NewEngine(store, nil)
	engine.AddProvider(&mockProvider{name: "test", records: nil})
	_ = engine.DiscoverNow(ctx)

	if svc, _ := store.Get(ctx, "tool-a"); svc != nil {
		t.Error("provider-discovered service must still be removed when no longer reported")
	}
}

// TestEngine_DiscoverNowMergesRegisteredWithDiscovered: when a provider also
// reports the same endpoint as a manual registration, the discovery cycle
// must not clobber the registered service into removal — it updates it.
func TestEngine_DiscoverNowMergesRegisteredWithDiscovered(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(store, nil)
	ctx := context.Background()

	engine.AddProvider(&mockProvider{
		name:    "test",
		records: []DiscoveryRecord{{Source: "test", Endpoint: "tool-a"}},
	})
	_ = engine.DiscoverNow(ctx)
	_ = engine.Register(ctx, RegisterRequest{Name: "tool-a", Endpoint: "tool-a", Tags: []string{"manual"}})

	// Next cycle: provider still reports tool-a; the registered record and
	// the provider record describe the same endpoint (normalized "tool-a").
	_ = engine.DiscoverNow(ctx)

	svc, err := store.Get(ctx, "tool-a")
	if err != nil || svc == nil {
		t.Fatal("service merged from register+provider disappeared")
	}
}
