package taskfabric

// QuantumStep is one Agent Step executed inside a quantum (design §5 of
// ares-runtime.md): reasoning → tool call → observation. It returns the
// durable checkpoint (progress so far) and whether the task is complete.
type QuantumStep func() (checkpoint any, done bool, err error)

// RunQuantum executes a single execution quantum: start the leased task, run
// one agent step, then decide at the quantum boundary (yield is the execution
// boundary, not a state decision):
//
//	done  → COMPLETED (preserving the checkpoint's worker result)
//	err   → FAILED (or requeued to READY per retry policy)
//	!done → SUSPENDED with the checkpoint preserved
//
// SUSPENDED semantics (2026-08-16 lock, ares-runtime.md §SUSPENDED 语义锁定):
// the agent's execution quantum ended, but the task's durable intent is NOT
// yet complete — this is not "the agent was suspended". Task suspended ≠
// Agent suspended ≠ Execution yielded. Continue is the Scheduler's decision
// via re-acquire; the state machine never does RUNNING→RUNNING directly. The
// fencing token (epoch) is verified on every step so a stale holder cannot
// drive a task it no longer owns.
//
// The done→COMPLETED path preserves the checkpoint (worker result) so the
// kernel dispatch can read it back: kernelTaskDispatcher.Dispatch subscribes
// to EventTaskCompleted, infers the task is done, then reads the completed
// task's Checkpoint for the worker's real items/reason (serve result-reflux).
//
// Args:
//   - taskID: the task id.
//   - agentID: the executing agent (must hold the lease).
//   - epoch: the fencing token returned by Acquire.
//   - step: the agent step to run inside this quantum.
//
// Returns:
//   - error: ErrNotOwner / ErrEpochMismatch / ErrIllegalState, or the
//     outcome of the quantum transition.
func (f *Fabric) RunQuantum(taskID, agentID string, epoch uint64, step QuantumStep) error {
	if err := f.Start(taskID, agentID, epoch); err != nil {
		return err
	}
	checkpoint, done, stepErr := step()
	if stepErr != nil {
		return f.Fail(taskID, agentID, epoch)
	}
	if done {
		// Preserve the step's output in the task so the kernel dispatch
		// can read it back. A nil checkpoint means the quantum produced no
		// output (pure state machine progress) — plain Complete still works.
		if checkpoint != nil {
			return f.CompleteWithCheckpoint(taskID, agentID, epoch, checkpoint)
		}
		return f.Complete(taskID, agentID, epoch)
	}
	return f.Yield(taskID, agentID, epoch, checkpoint)
}
