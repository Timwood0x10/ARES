package taskfabric

import "sync"

// AgentQueue is one agent's local ready-queue (design §5 of ares-runtime.md:
// per-worker queues — the substrate for work stealing; no central dispatch
// bottleneck). It holds READY task ids queued to this agent.
type AgentQueue struct {
	AgentID string
	mu      sync.Mutex
	tasks   []string
}

// NewAgentQueue creates an empty queue for an agent.
func NewAgentQueue(agentID string) *AgentQueue {
	return &AgentQueue{AgentID: agentID}
}

// Enqueue appends a task id to the queue.
func (q *AgentQueue) Enqueue(taskID string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, taskID)
}

// Len returns the number of queued tasks.
func (q *AgentQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.tasks)
}

// Steal takes the first task from another agent's queue that this agent is
// capable of executing — capability-aware stealing (design §8): not "whoever
// is idle", but "who is the best executor". D2 (2026-08-16): the Scheduler
// orchestrates uniformly — an idle agent steals from a busy queue, then
// acquires the task. Tasks the stealer cannot handle are skipped, never
// stolen.
//
// Args:
//   - from: the queue being stolen from.
//   - capabilities: the stealer's declared capabilities.
//   - capabilityOf: maps a task id to its required capability.
//
// Returns:
//   - string: the stolen task id ("" when nothing stealable).
//   - bool: whether a task was stolen.
func (q *AgentQueue) Steal(from *AgentQueue, capabilities []string, capabilityOf func(string) string) (string, bool) {
	from.mu.Lock()
	defer from.mu.Unlock()
	for i, taskID := range from.tasks {
		if Score(capabilityOf(taskID), Candidate{Capabilities: capabilities, Confidence: 1}) <= 0 {
			continue // not capable: leave it for a capable owner
		}
		from.tasks = append(from.tasks[:i], from.tasks[i+1:]...)
		return taskID, true
	}
	return "", false
}
