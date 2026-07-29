package workflow

import (
	"fmt"
	"sort"
	"time"
)

// PendingInterrupt is the durable unresolved HITL state for one node.
type PendingInterrupt struct {
	Token     string    `json:"token"`
	NodeID    NodeID    `json:"node_id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// SetPendingInterrupt records an unresolved human approval point.
func (s *ExecutionScope) SetPendingInterrupt(interrupt PendingInterrupt) {
	if interrupt.Token == "" {
		interrupt.Token = interruptToken(s.ExecutionID, interrupt.NodeID)
	}
	s.interruptMu.Lock()
	s.pendingInterrupts[interrupt.NodeID] = interrupt
	s.interruptMu.Unlock()
}

func interruptToken(executionID string, nodeID NodeID) string {
	return fmt.Sprintf("%s:%s", executionID, nodeID)
}

// PendingInterrupt returns one unresolved human approval point.
func (s *ExecutionScope) PendingInterrupt(nodeID NodeID) (PendingInterrupt, bool) {
	s.interruptMu.RLock()
	defer s.interruptMu.RUnlock()
	interrupt, ok := s.pendingInterrupts[nodeID]
	return interrupt, ok
}

// ResolvePendingInterrupt removes a resolved human approval point.
func (s *ExecutionScope) ResolvePendingInterrupt(nodeID NodeID) {
	s.interruptMu.Lock()
	delete(s.pendingInterrupts, nodeID)
	s.interruptMu.Unlock()
}

// PendingInterrupts returns a durable snapshot of unresolved approval points.
func (s *ExecutionScope) PendingInterrupts() []PendingInterrupt {
	s.interruptMu.RLock()
	defer s.interruptMu.RUnlock()
	result := make([]PendingInterrupt, 0, len(s.pendingInterrupts))
	for _, interrupt := range s.pendingInterrupts {
		result = append(result, interrupt)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].NodeID < result[j].NodeID
	})
	return result
}

// RestorePendingInterrupts replaces unresolved approval points during resume.
func (s *ExecutionScope) RestorePendingInterrupts(interrupts []PendingInterrupt) {
	s.interruptMu.Lock()
	s.pendingInterrupts = make(map[NodeID]PendingInterrupt, len(interrupts))
	for _, interrupt := range interrupts {
		s.pendingInterrupts[interrupt.NodeID] = interrupt
	}
	s.interruptMu.Unlock()
}

// RecordMutationID records one successfully applied mutation.
func (s *ExecutionScope) RecordMutationID(id string) {
	if id == "" {
		return
	}
	s.mutationMu.Lock()
	s.mutationIDs = append(s.mutationIDs, id)
	s.mutationMu.Unlock()
}

// RemoveMutationIDs rolls back the last count uncommitted mutation IDs.
func (s *ExecutionScope) RemoveMutationIDs(count int) {
	if count <= 0 {
		return
	}
	s.mutationMu.Lock()
	if count >= len(s.mutationIDs) {
		s.mutationIDs = nil
	} else {
		s.mutationIDs = s.mutationIDs[:len(s.mutationIDs)-count]
	}
	s.mutationMu.Unlock()
}

// MutationIDs returns applied mutation IDs in commit order.
func (s *ExecutionScope) MutationIDs() []string {
	s.mutationMu.RLock()
	defer s.mutationMu.RUnlock()
	return append([]string(nil), s.mutationIDs...)
}

// RestoreMutationIDs restores applied mutation IDs during resume.
func (s *ExecutionScope) RestoreMutationIDs(ids []string) {
	s.mutationMu.Lock()
	s.mutationIDs = append([]string(nil), ids...)
	s.mutationMu.Unlock()
}
