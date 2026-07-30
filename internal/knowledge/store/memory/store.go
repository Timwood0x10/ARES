// Package memorystore provides an in-memory KnowledgeStore implementation.
package memorystore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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

	// Apply offset. When the offset falls at or beyond the end of the
	// result set, return an empty page rather than silently ignoring the
	// offset and returning the full result.
	if q.Offset > 0 {
		if q.Offset >= len(result) {
			result = nil
		} else {
			result = result[q.Offset:]
		}
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

// HybridSearch performs vector + lexical scoring over in-memory objects.
func (s *Store) HybridSearch(_ context.Context, req knowledge.HybridSearchRequest) ([]knowledge.ScoredObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Default status filter: active (plus empty-status objects for back-compat).
	statuses := req.StatusFilter
	if len(statuses) == 0 {
		statuses = []knowledge.ObjectStatus{knowledge.StatusActive}
	}

	// Collect candidates matching namespace/types/status.
	var candidates []*knowledge.KnowledgeObject
	for _, obj := range s.objects {
		if req.Namespace != "" && obj.Namespace != req.Namespace {
			continue
		}
		if len(req.Types) > 0 {
			typeMatch := false
			for _, t := range req.Types {
				if obj.Type == t {
					typeMatch = true
					break
				}
			}
			if !typeMatch {
				continue
			}
		}
		if !statusMatches(obj.Status, statuses) {
			continue
		}
		candidates = append(candidates, obj)
	}

	// Build the representations map for the requested model.
	reps := make(map[string]*knowledge.Representation, len(candidates))
	for _, obj := range candidates {
		key := obj.ID + ":" + req.Model
		if rep, ok := s.reps[key]; ok {
			reps[obj.ID] = rep
		}
	}

	scored := knowledge.ScoreHybrid(candidates, reps, req.QueryVector, req.Query)

	// Filter by MinScore.
	if req.MinScore > 0 {
		filtered := scored[:0]
		for _, r := range scored {
			if r.FinalScore >= req.MinScore {
				filtered = append(filtered, r)
			}
		}
		scored = filtered
	}

	// Sort by FinalScore descending.
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].FinalScore > scored[j].FinalScore
	})

	// Apply recall cap (TopK) and final cap (FinalK).
	topK := req.TopK
	if topK <= 0 {
		topK = 20
	}
	if len(scored) > topK {
		scored = scored[:topK]
	}
	finalK := req.FinalK
	if finalK <= 0 {
		finalK = 5
	}
	if len(scored) > finalK {
		scored = scored[:finalK]
	}
	return scored, nil
}

// ListByStatus returns objects in ns matching one of the given statuses.
// Empty status matches objects with no status (backward compatibility).
func (s *Store) ListByStatus(_ context.Context, ns string, status knowledge.ObjectStatus, limit int) ([]*knowledge.KnowledgeObject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*knowledge.KnowledgeObject
	for _, obj := range s.objects {
		if ns != "" && obj.Namespace != ns {
			continue
		}
		if obj.Status != status {
			// Empty object status is treated as active for back-compat: only
			// skip when the requested status is not active, or the object has a
			// (non-empty) status that differs from it.
			if status != knowledge.StatusActive || obj.Status != "" {
				continue
			}
		}
		result = append(result, obj)
		if limit > 0 && len(result) >= limit {
			break
		}
	}
	return result, nil
}

// UpdateStatus transitions an object's lifecycle status.
func (s *Store) UpdateStatus(_ context.Context, id string, status knowledge.ObjectStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	obj.Status = status
	obj.UpdatedAt = time.Now().UTC()
	return nil
}

// Promote moves a candidate to active and records its computed Quality.
func (s *Store) Promote(_ context.Context, id string, q *knowledge.Quality) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	obj.Status = knowledge.StatusActive
	obj.Quality = q
	obj.UpdatedAt = time.Now().UTC()
	return nil
}

// statusMatches reports whether objStatus matches any of the wanted statuses,
// treating an empty objStatus as active (backward compatibility).
func statusMatches(objStatus knowledge.ObjectStatus, want []knowledge.ObjectStatus) bool {
	for _, w := range want {
		if objStatus == w {
			return true
		}
		// Empty object status is treated as active for back-compat.
		if w == knowledge.StatusActive && objStatus == "" {
			return true
		}
	}
	return false
}

// Count returns the number of stored objects.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.objects)
}
