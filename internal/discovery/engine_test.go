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
