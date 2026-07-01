package discovery

import (
	"context"
	"testing"
)

func TestMemoryStore_SaveAndGet(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	svc := &DiscoveredService{
		Identity: ServiceIdentity{ID: "tool-a", Name: "tool-a"},
	}
	if err := store.Save(ctx, svc); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := store.Get(ctx, "tool-a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Identity.Name != "tool-a" {
		t.Errorf("expected name 'tool-a', got %q", got.Identity.Name)
	}
}

func TestMemoryStore_GetNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	got, err := store.Get(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent key")
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestMemoryStore_List(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, &DiscoveredService{Identity: ServiceIdentity{ID: "a"}})
	_ = store.Save(ctx, &DiscoveredService{Identity: ServiceIdentity{ID: "b"}})

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 services, got %d", len(list))
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.Save(ctx, &DiscoveredService{Identity: ServiceIdentity{ID: "a"}})
	_ = store.Delete(ctx, "a")

	got, _ := store.Get(ctx, "a")
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestMemoryStore_SaveNilIgnored(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Should not panic.
	_ = store.Save(ctx, nil)
	_ = store.Save(ctx, &DiscoveredService{})
}

func TestMemoryStore_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	orig := &DiscoveredService{
		Identity: ServiceIdentity{ID: "a", Tags: []string{"x"}},
	}
	_ = store.Save(ctx, orig)

	// Mutate original after save.
	orig.Identity.Tags = append(orig.Identity.Tags, "y")

	// Stored copy should not be affected.
	got, _ := store.Get(ctx, "a")
	if len(got.Identity.Tags) != 1 {
		t.Errorf("expected 1 tag (deep copy), got %d", len(got.Identity.Tags))
	}
}
