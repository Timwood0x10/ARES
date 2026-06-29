package discovery

import (
	"context"
	"sync"
)

// MemoryStore is an in-memory ServiceStore for development and testing.
type MemoryStore struct {
	mu       sync.RWMutex
	services map[string]*DiscoveredService
}

// NewMemoryStore creates a new in-memory service store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		services: make(map[string]*DiscoveredService),
	}
}

func (s *MemoryStore) Save(_ context.Context, svc *DiscoveredService) error {
	if svc == nil || svc.Identity.ID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *svc
	s.services[svc.Identity.ID] = &cp
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*DiscoveredService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[id]
	if !ok {
		return nil, nil
	}
	cp := *svc
	return &cp, nil
}

func (s *MemoryStore) List(_ context.Context) ([]*DiscoveredService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*DiscoveredService, 0, len(s.services))
	for _, svc := range s.services {
		cp := *svc
		result = append(result, &cp)
	}
	return result, nil
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.services, id)
	return nil
}
