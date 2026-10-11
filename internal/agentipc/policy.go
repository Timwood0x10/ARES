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

// KernelDispatcher holds the kernel's dispatch path so the Kernel can swap it
// at runtime: enableKernelExecution replaces the scoring-only path with the
// submitting one via SetNewPath.
//
// It does not route by an execution-policy flag and does not run an inactive
// track in shadow — the legacy leader track and the former PolicyFlag / shadow
// facade were removed along with the dispatch entry that consumed them. The
// only live state is the current path. Renamed from DualTrackDispatcher in
// v0.3.3: with a single track left, "dual" was a lie.
type KernelDispatcher struct {
	mu      sync.Mutex // guards newPath
	newPath Dispatcher
}

// NewKernelDispatcher wires the kernel dispatcher with newPath as the current
// path. A nil newPath is valid: scoring-only kernels run that way until
// enableKernelExecution attaches a submitting path.
func NewKernelDispatcher(newPath Dispatcher) *KernelDispatcher {
	return &KernelDispatcher{newPath: newPath}
}

// SetNewPath swaps the dispatcher at runtime (used by the Kernel when the Task
// Fabric execution path is enabled: the scoring-only path is replaced by the
// real executor).
func (d *KernelDispatcher) SetNewPath(newPath Dispatcher) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.newPath = newPath
}

// NewPath returns the current new-path dispatcher (may be nil when not wired).
func (d *KernelDispatcher) NewPath() Dispatcher {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.newPath
}
