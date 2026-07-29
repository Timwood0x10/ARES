package workflow

import (
	"fmt"
	"sync"
)

// PatchQueue stores execution-scoped mutations until a Runner safe point commits them.
type PatchQueue struct {
	mu      sync.Mutex
	pending map[string][]Mutation
	known   map[string]map[string]bool
}

// NewPatchQueue creates an empty concurrent mutation queue.
func NewPatchQueue() *PatchQueue {
	return &PatchQueue{
		pending: make(map[string][]Mutation),
		known:   make(map[string]map[string]bool),
	}
}

// Enqueue appends a unique mutation for one execution.
func (q *PatchQueue) Enqueue(executionID string, mutation Mutation) error {
	if q == nil {
		return fmt.Errorf("patch queue must not be nil")
	}
	if executionID == "" {
		return fmt.Errorf("execution ID must not be empty")
	}
	if mutation.ID == "" {
		return fmt.Errorf("mutation ID must not be empty")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.known[executionID] == nil {
		q.known[executionID] = make(map[string]bool)
	}
	if q.known[executionID][mutation.ID] {
		return fmt.Errorf("mutation %q is already queued for execution %q", mutation.ID, executionID)
	}
	q.pending[executionID] = append(q.pending[executionID], cloneMutation(mutation))
	q.known[executionID][mutation.ID] = true
	return nil
}

// Pending returns an immutable snapshot in enqueue order.
func (q *PatchQueue) Pending(executionID string) []Mutation {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return cloneMutations(q.pending[executionID])
}

// Restore replaces pending mutations for one resumed execution.
func (q *PatchQueue) Restore(executionID string, mutations []Mutation) error {
	if q == nil {
		return fmt.Errorf("patch queue must not be nil")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	known := make(map[string]bool, len(mutations))
	for _, mutation := range mutations {
		if mutation.ID == "" {
			return fmt.Errorf("restored mutation ID must not be empty")
		}
		if known[mutation.ID] {
			return fmt.Errorf("restored mutation %q is duplicated", mutation.ID)
		}
		known[mutation.ID] = true
	}
	q.pending[executionID] = cloneMutations(mutations)
	q.known[executionID] = known
	return nil
}

// Acknowledge removes an atomically committed queue prefix.
func (q *PatchQueue) Acknowledge(executionID string, ids []string) error {
	if q == nil || len(ids) == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	pending := q.pending[executionID]
	if len(pending) < len(ids) {
		return fmt.Errorf("acknowledge %d mutations with only %d pending", len(ids), len(pending))
	}
	for index, id := range ids {
		if pending[index].ID != id {
			return fmt.Errorf("mutation acknowledgement %q does not match queue head %q", id, pending[index].ID)
		}
		delete(q.known[executionID], id)
	}
	q.pending[executionID] = append([]Mutation(nil), pending[len(ids):]...)
	if len(q.pending[executionID]) == 0 {
		delete(q.pending, executionID)
		delete(q.known, executionID)
	}
	return nil
}

func cloneMutations(mutations []Mutation) []Mutation {
	result := make([]Mutation, len(mutations))
	for index := range mutations {
		result[index] = cloneMutation(mutations[index])
	}
	return result
}

func cloneMutation(mutation Mutation) Mutation {
	clone := mutation
	if mutation.Node != nil {
		node := *mutation.Node
		node.Metadata = cloneStringMap(mutation.Node.Metadata)
		clone.Node = &node
	}
	if mutation.Edge != nil {
		edge := *mutation.Edge
		clone.Edge = &edge
	}
	if mutation.Policy != nil {
		policy := *mutation.Policy
		clone.Policy = &policy
	}
	return clone
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
