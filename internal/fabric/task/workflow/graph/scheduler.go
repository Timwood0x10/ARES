// package graph - provides dynamic agent orchestration with pluggable scheduling.
//
// TODO(tech-debt): the priority / short-job / round-robin / weighted-fair
// selectors were removed as unreachable dead code — no production caller and no
// examples user; the graph always runs DefaultScheduler, and SetScheduler stays
// as the injection seam for a custom Scheduler. Re-introduce a concrete
// selector behind that seam only when a real scheduling-policy requirement
// lands (do not re-add them speculatively).

package graph

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
