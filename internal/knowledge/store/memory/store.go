package memorystore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

var (
	// ErrObjectNotFound is returned when a Get call finds no matching object.
	ErrObjectNotFound = errors.New("object not found")
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

// cloneObject deep-copies a KnowledgeObject. The in-memory store hands out
// and accepts pointers, so without a copy at BOTH boundaries the stored
// object aliases caller-owned slices/maps: a caller mutating an object after
// Save (or through a previously returned pointer) races with every reader
// under RLock. SQL-backed stores are naturally isolated (each row scan
// allocates fresh values); the copy restores that contract here.
func cloneObject(obj *knowledge.KnowledgeObject) *knowledge.KnowledgeObject {
	if obj == nil {
		return nil
	}
	cp := *obj
	cp.Raw = append([]byte(nil), obj.Raw...)
	if obj.Metadata != nil {
		cp.Metadata = make(map[string]any, len(obj.Metadata))
		for k, v := range obj.Metadata {
			cp.Metadata[k] = v
		}
	}
	if obj.Tags != nil {
		cp.Tags = append([]string(nil), obj.Tags...)
	}
	if obj.Evidence != nil {
		cp.Evidence = append([]knowledge.Evidence(nil), obj.Evidence...)
	}
	if obj.Representations != nil {
		cp.Representations = make(map[string]string, len(obj.Representations))
		for k, v := range obj.Representations {
			cp.Representations[k] = v
		}
	}
	if obj.Quality != nil {
		q := *obj.Quality
		cp.Quality = &q
	}
	if obj.Relations != nil {
		cp.Relations = append([]knowledge.Relation(nil), obj.Relations...)
	}
	return &cp
}

// cloneRepresentation deep-copies a Representation (Vector slice, Metadata
// map) — same isolation contract as cloneObject.
func cloneRepresentation(rep *knowledge.Representation) *knowledge.Representation {
	if rep == nil {
		return nil
	}
	cp := *rep
	if rep.Vector != nil {
		cp.Vector = append([]float32(nil), rep.Vector...)
	}
	if rep.Metadata != nil {
		cp.Metadata = make(map[string]string, len(rep.Metadata))
		for k, v := range rep.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp
}

func (s *Store) Save(_ context.Context, objects ...*knowledge.KnowledgeObject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, obj := range objects {
		if obj.ID == "" {
			return errors.New("knowledge object ID cannot be empty")
		}
		// Store a private copy so later caller mutations cannot corrupt the
		// stored state (see cloneObject).
		s.objects[obj.ID] = cloneObject(obj)
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
	return cloneObject(obj), nil
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
		result = append(result, cloneObject(obj))
	}

	// Sort by confidence descending.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Confidence > result[j].Confidence
	})

	// Apply offset first, then limit (LIMIT/OFFSET semantics). The previous
	// order (limit-then-offset) produced wrong pagination: any offset beyond
	// the limit returned an empty page, and pages after the first were never
	// reachable.
	if q.Offset > 0 {
		if q.Offset >= len(result) {
			return nil, nil
		}
		result = result[q.Offset:]
	}
	if q.Limit > 0 && len(result) > q.Limit {
		result = result[:q.Limit]
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
			scored = append(scored, cloneObject(obj))
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
	s.reps[key] = cloneRepresentation(rep)
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
	return cloneRepresentation(rep), nil
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
	// The results embed pointers to the STORED objects; hand the caller
	// private copies so it cannot mutate store state through a returned
	// pointer (see cloneObject). The reps map above is internal-only and
	// never escapes.
	for i := range scored {
		scored[i].Object = cloneObject(scored[i].Object)
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
		result = append(result, cloneObject(obj))
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
	// Copy the caller's Quality: storing the pointer verbatim would alias
	// caller-owned memory (same isolation contract as cloneObject).
	if q != nil {
		qc := *q
		obj.Quality = &qc
	} else {
		obj.Quality = nil
	}
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
