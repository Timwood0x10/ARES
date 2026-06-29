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
	s.services[svc.Identity.ID] = deepCopyService(svc)
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*DiscoveredService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[id]
	if !ok {
		return nil, nil
	}
	return deepCopyService(svc), nil
}

func (s *MemoryStore) List(_ context.Context) ([]*DiscoveredService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*DiscoveredService, 0, len(s.services))
	for _, svc := range s.services {
		result = append(result, deepCopyService(svc))
	}
	return result, nil
}

// deepCopyService creates a deep copy of a DiscoveredService.
func deepCopyService(svc *DiscoveredService) *DiscoveredService {
	cp := *svc

	// Deep copy slices.
	cp.Records = make([]DiscoveryRecord, len(svc.Records))
	for i, r := range svc.Records {
		cp.Records[i] = deepCopyRecord(r)
	}

	// Deep copy Identity slices/maps.
	if svc.Identity.Tags != nil {
		cp.Identity.Tags = make([]string, len(svc.Identity.Tags))
		copy(cp.Identity.Tags, svc.Identity.Tags)
	}
	if svc.Identity.Metadata != nil {
		cp.Identity.Metadata = make(map[string]string, len(svc.Identity.Metadata))
		for k, v := range svc.Identity.Metadata {
			cp.Identity.Metadata[k] = v
		}
	}

	return &cp
}

// deepCopyRecord creates a deep copy of a DiscoveryRecord.
func deepCopyRecord(r DiscoveryRecord) DiscoveryRecord {
	cp := r
	if r.Args != nil {
		cp.Args = make([]string, len(r.Args))
		copy(cp.Args, r.Args)
	}
	if r.Tags != nil {
		cp.Tags = make([]string, len(r.Tags))
		copy(cp.Tags, r.Tags)
	}
	if r.Metadata != nil {
		cp.Metadata = make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			cp.Metadata[k] = v
		}
	}
	return cp
}

func (s *MemoryStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.services, id)
	return nil
}
