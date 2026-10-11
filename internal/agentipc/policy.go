package agentipc

import (
	"context"
	"sync"
)

// Dispatcher is the abstraction a kernel dispatch path implements. Production
// wires the Task Fabric path; the legacy leader path was removed.
// Implementations must be equivalent in observable outcome (the task is
// delivered and executed); only the path differs.
type Dispatcher interface {
	// D sends a task to an agent under the given policy. The
	// implementation must be equivalent in observable outcome (the task is
	// delivered and executed); only the path differs.
	D(ctx context.Context, agentID string, taskID string, payload any) error
}

// DualTrackDispatcher holds the kernel's dispatch path so the Kernel can swap
// it at runtime: enableKernelExecution replaces the scoring-only path with the
// submitting one via SetNewPath.
//
// It no longer routes by an execution-policy flag and no longer runs an
// inactive track in shadow — the legacy leader track was deleted, and the
// former PolicyFlag / shadow facade was removed along with the dispatch entry
// that consumed it. The only live state is the current path.
type DualTrackDispatcher struct {
	mu      sync.Mutex // guards newPath
	newPath Dispatcher
}

// NewDualTrackDispatcher wires the kernel dispatcher with newPath as the
// current path. A nil newPath is valid: scoring-only kernels run that way
// until enableKernelExecution attaches a submitting path.
func NewDualTrackDispatcher(newPath Dispatcher) *DualTrackDispatcher {
	return &DualTrackDispatcher{newPath: newPath}
}

// SetNewPath swaps the dispatcher at runtime (used by the Kernel when the Task
// Fabric execution path is enabled: the scoring-only path is replaced by the
// real executor).
func (d *DualTrackDispatcher) SetNewPath(newPath Dispatcher) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.newPath = newPath
}

// NewPath returns the current new-path dispatcher (may be nil when not wired).
func (d *DualTrackDispatcher) NewPath() Dispatcher {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.newPath
}
