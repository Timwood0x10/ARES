// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import "sync"

// Scheduler defines the interface for node scheduling.
type Scheduler interface {
	// Select returns the next node ID to execute from the ready queue.
	Select(ready []string) string
}

// DefaultScheduler provides FIFO scheduling, consistent with Workflow Engine.
type DefaultScheduler struct{}

// NewDefaultScheduler creates a new default scheduler.
func NewDefaultScheduler() *DefaultScheduler {
	return &DefaultScheduler{}
}

// Select returns the first ready node (FIFO).
func (s *DefaultScheduler) Select(ready []string) string {
	if len(ready) == 0 {
		return ""
	}
	return ready[0]
}

// PriorityScheduler provides priority-based scheduling.
type PriorityScheduler struct {
	priorities map[string]int
}

// NewPriorityScheduler creates a new priority scheduler.
func NewPriorityScheduler(priorities map[string]int) *PriorityScheduler {
	if priorities == nil {
		priorities = make(map[string]int)
	}
	return &PriorityScheduler{priorities: priorities}
}

// Select returns the ready node with the highest priority.
func (s *PriorityScheduler) Select(ready []string) string {
	if len(ready) == 0 {
		return ""
	}

	bestNode := ready[0]
	bestPriority := s.getPriority(bestNode)

	for _, nodeID := range ready[1:] {
		priority := s.getPriority(nodeID)
		if priority > bestPriority {
			bestNode = nodeID
			bestPriority = priority
		}
	}

	return bestNode
}

// getPriority returns the priority for a node ID, defaulting to 0.
func (s *PriorityScheduler) getPriority(nodeID string) int {
	if s == nil || s.priorities == nil {
		return 0
	}
	priority, ok := s.priorities[nodeID]
	if !ok {
		return 0
	}
	return priority
}

// ShortJobScheduler provides shortest-job-first scheduling.
type ShortJobScheduler struct {
	estimates map[string]int // estimated latency in milliseconds
}

// NewShortJobScheduler creates a new short-job scheduler.
func NewShortJobScheduler(estimates map[string]int) *ShortJobScheduler {
	if estimates == nil {
		estimates = make(map[string]int)
	}
	return &ShortJobScheduler{estimates: estimates}
}

// Select returns the ready node with the shortest estimated execution time.
func (s *ShortJobScheduler) Select(ready []string) string {
	if len(ready) == 0 {
		return ""
	}

	bestNode := ready[0]
	bestEstimate := s.getEstimate(bestNode)

	for _, nodeID := range ready[1:] {
		estimate := s.getEstimate(nodeID)
		if estimate < bestEstimate {
			bestNode = nodeID
			bestEstimate = estimate
		}
	}

	return bestNode
}

// getEstimate returns the estimated latency for a node ID.
// For unknown nodes, returns a reasonable default value (1000ms) to ensure
// they can still be scheduled but with lower priority than known short jobs.
func (s *ShortJobScheduler) getEstimate(nodeID string) int {
	if s == nil || s.estimates == nil {
		return 1000
	}
	estimate, ok := s.estimates[nodeID]
	if !ok {
		return 1000
	}
	return estimate
}

// RoundRobinScheduler cycles through ready nodes in order, distributing
// execution fairly across all ready tasks. Each call to Select advances
// the internal cursor by one position.
type RoundRobinScheduler struct {
	mu     sync.Mutex
	cursor int
}

// NewRoundRobinScheduler creates a new round-robin scheduler with cursor at 0.
func NewRoundRobinScheduler() *RoundRobinScheduler {
	return &RoundRobinScheduler{}
}

// Select returns the next ready node in round-robin order.
func (s *RoundRobinScheduler) Select(ready []string) string {
	if len(ready) == 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cursor >= len(ready) {
		s.cursor = 0
	}
	node := ready[s.cursor]
	s.cursor++
	return node
}

// WeightedFairScheduler distributes execution proportionally to each node's
// configured weight. Nodes with higher weight are selected more frequently.
// When all weights are equal, it behaves like RoundRobin.
type WeightedFairScheduler struct {
	mu      sync.Mutex
	weights map[string]int
	counter map[string]int // deficit counter per node
}

// NewWeightedFairScheduler creates a weighted fair scheduler.
// Nodes not in the weights map default to weight 1.
func NewWeightedFairScheduler(weights map[string]int) *WeightedFairScheduler {
	if weights == nil {
		weights = make(map[string]int)
	}
	return &WeightedFairScheduler{
		weights: weights,
		counter: make(map[string]int),
	}
}

// Select picks the ready node with the highest deficit (accumulated
// wait time relative to its weight), implementing weighted fair queuing.
func (s *WeightedFairScheduler) Select(ready []string) string {
	if len(ready) == 0 {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find the node with maximum deficit.
	var bestNode string
	maxDeficit := -1.0
	for _, nodeID := range ready {
		weight := s.weights[nodeID]
		if weight <= 0 {
			weight = 1
		}
		def := float64(s.counter[nodeID]) / float64(weight)
		s.counter[nodeID]++ // accumulate deficit
		if def > maxDeficit {
			maxDeficit = def
			bestNode = nodeID
		}
	}
	return bestNode
}
