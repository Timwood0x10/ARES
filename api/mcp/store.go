package mcp

import (
	"context"
	"sync"
	"time"
)

// MemoryStore is an in-memory implementation of ServiceStore.
// For production, implement ServiceStore with SQLite or Postgres.
type MemoryStore struct {
	mu       sync.RWMutex
	services map[string]*MCPService
}

// NewMemoryStore creates a new in-memory service store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{services: make(map[string]*MCPService)}
}

func (s *MemoryStore) Save(_ context.Context, svc *MCPService) error {
	if svc == nil || svc.ID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.services[svc.ID] = svc
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (*MCPService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	svc, ok := s.services[id]
	if !ok {
		return nil, nil
	}
	cp := *svc
	return &cp, nil
}

func (s *MemoryStore) List(_ context.Context) ([]*MCPService, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*MCPService, 0, len(s.services))
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

func (s *MemoryStore) UpdateStats(_ context.Context, id string, success bool, latency time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	svc, ok := s.services[id]
	if !ok {
		return nil
	}
	svc.CallCount++
	if !success {
		svc.ErrorCount++
	}
	// Exponential moving average for latency
	if svc.AvgLatency == 0 {
		svc.AvgLatency = latency
	} else {
		svc.AvgLatency = time.Duration(float64(svc.AvgLatency)*0.8 + float64(latency)*0.2)
	}
	if svc.CallCount > 0 {
		svc.SuccessRate = 1.0 - float64(svc.ErrorCount)/float64(svc.CallCount)
	}
	return nil
}
