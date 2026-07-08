// Package memorystore provides an in-memory KnowledgeStore implementation.
package memorystore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

var (
	// ErrObjectNotFound is returned when a Get call finds no matching object.
	ErrObjectNotFound = fmt.Errorf("object not found")
)

// Store is an in-memory implementation of KnowledgeStore.
// Thread-safe, suitable for testing and single-node deployments.
type Store struct {
	mu      sync.RWMutex
	objects map[string]*knowledge.KnowledgeObject
	reps    map[string]*knowledge.Representation // key: objectID:model
}

// New creates a new in-memory KnowledgeStore.
func New() *Store {
	return &Store{
		objects: make(map[string]*knowledge.KnowledgeObject),
		reps:    make(map[string]*knowledge.Representation),
	}
}

func (s *Store) Save(_ context.Context, objects ...*knowledge.KnowledgeObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, obj := range objects {
		if obj.ID == "" {
			return fmt.Errorf("knowledge object ID cannot be empty")
		}
		s.objects[obj.ID] = obj
	}
	return nil
}

func (s *Store) Get(_ context.Context, id string) (*knowledge.KnowledgeObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	obj, ok := s.objects[id]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return obj, nil
}

func (s *Store) Query(_ context.Context, q knowledge.Query) ([]*knowledge.KnowledgeObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*knowledge.KnowledgeObject
	for _, obj := range s.objects {
		if q.Namespace != "" && obj.Namespace != q.Namespace {
			continue
		}
		if len(q.Types) > 0 {
			typeMatch := false
			for _, t := range q.Types {
				if obj.Type == t {
					typeMatch = true
					break
				}
			}
			if !typeMatch {
				continue
			}
		}
		if len(q.Tags) > 0 {
			tagMatch := false
			for _, t := range q.Tags {
				for _, ot := range obj.Tags {
					if strings.EqualFold(t, ot) {
						tagMatch = true
						break
					}
				}
				if tagMatch {
					break
				}
			}
			if !tagMatch {
				continue
			}
		}
		result = append(result, obj)
	}

	// Sort by confidence descending.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Confidence > result[j].Confidence
	})

	if q.Limit > 0 && len(result) > q.Limit {
		result = result[:q.Limit]
	}

	// Apply offset.
	if q.Offset > 0 && q.Offset < len(result) {
		result = result[q.Offset:]
	}

	return result, nil
}

func (s *Store) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, id)
	// Clean up related representations.
	for key := range s.reps {
		if strings.HasPrefix(key, id+":") {
			delete(s.reps, key)
		}
	}
	return nil
}

func (s *Store) Search(_ context.Context, text string, model string, limit int) ([]*knowledge.KnowledgeObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Simple keyword-based search for in-memory store.
	text = strings.ToLower(text)
	keywords := strings.Fields(text)

	var scored []*knowledge.KnowledgeObject
	for _, obj := range s.objects {
		content := strings.ToLower(obj.Summary + " " + strings.Join(obj.Tags, " "))
		score := 0
		for _, kw := range keywords {
			if strings.Contains(content, kw) {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, obj)
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Confidence > scored[j].Confidence
	})

	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}

	return scored, nil
}

func (s *Store) SaveRepresentation(_ context.Context, rep *knowledge.Representation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := rep.ObjectID + ":" + rep.Model
	s.reps[key] = rep
	return nil
}

func (s *Store) GetRepresentation(_ context.Context, objectID string, model string) (*knowledge.Representation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := objectID + ":" + model
	rep, ok := s.reps[key]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return rep, nil
}

// Count returns the number of stored objects.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.objects)
}
