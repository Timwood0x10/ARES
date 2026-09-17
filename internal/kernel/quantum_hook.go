package kernel

import (
	"context"
)

// QuantumHook is the extension contract at the quantum boundary. The scheduler
// invokes it around every RunQuantum so observational plugins (metrics,
// audit, tool allowlist) can participate in scheduling without the kernel
// depending on any concrete plugin package (the interface
// lives with the consumer).
//
// Contract:
//   - Hooks are observational: an error from BeforeQuantum is logged and does
//     NOT abort the quantum (the fabric owns retry/failure semantics).
//   - Hooks must be safe for concurrent invocation: one goroutine per ready
//     task drains in parallel (bounded by maxConcurrent).
//   - Hooks must be fast and non-blocking: they run inside the drain loop on
//     the scheduling hot path; long-running work belongs in a plugin's own
//     goroutine.
type QuantumHook interface {
	// BeforeQuantum runs before the winner executes its quantum.
	//
	// Args:
	//   - ctx: cancellation propagates from the drain loop.
	//   - taskID: the fabric task about to run.
	//   - agentID: the winning executor.
	//
	// Returns:
	//   - error: logged by the scheduler; never blocks the quantum.
	BeforeQuantum(ctx context.Context, taskID, agentID string) error

	// AfterQuantum runs after the quantum finalizes (success or failure).
	//
	// Args:
	//   - ctx: cancellation propagates from the drain loop.
	//   - taskID: the fabric task that ran.
	//   - agentID: the executor that ran it.
	//   - err: the quantum outcome (nil on success).
	AfterQuantum(ctx context.Context, taskID, agentID string, err error)
}

// WithQuantumHook attaches an observational hook at the quantum boundary.
// Nil clears the hook (backward compatible default: no hook).
// Returns the scheduler for chaining.
func (s *Scheduler) WithQuantumHook(h QuantumHook) *Scheduler {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.quantumHook = h
	return s
}

// quantumHookOf snapshots the currently wired hook under execMu so a runtime
// WithQuantumHook call cannot race with the drain loop reading the field.
func (s *Scheduler) quantumHookOf() QuantumHook {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	return s.quantumHook
}

// beforeQuantum invokes the hook if wired. Never blocks scheduling: hook
// errors are logged and swallowed (observational contract above).
func (s *Scheduler) beforeQuantum(ctx context.Context, taskID, agentID string) {
	h := s.quantumHookOf()
	if h == nil {
		return
	}
	if err := h.BeforeQuantum(ctx, taskID, agentID); err != nil {
		log.Error("kernel scheduler: beforeQuantum hook error (continuing)", "task_id", taskID, "agent", agentID, "error", err)
	}
}

// afterQuantum invokes the hook if wired. Never blocks scheduling.
func (s *Scheduler) afterQuantum(ctx context.Context, taskID, agentID string, err error) {
	h := s.quantumHookOf()
	if h == nil {
		return
	}
	h.AfterQuantum(ctx, taskID, agentID, err)
}
