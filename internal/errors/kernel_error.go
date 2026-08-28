package errors

import "strings"

// KernelError is a structured error for the kernel path (scheduling, fabric,
// IPC). It implements the standard error interface and preserves the unwrap
// chain, so it can be emitted directly by structured logging with precise
// task/agent/op/code attribution for fast production triage.
//
// Field conventions (see ares-repair-plan-zh.md §2.5):
//   - Op:      operation name, e.g. "schedule", "acquire", "run_quantum".
//   - Code:    machine-readable error code, e.g. "no_capable_candidate".
//   - TaskID:  the affected task (empty means not task-scoped).
//   - AgentID: the affected agent (empty means not agent-scoped).
//   - Err:     the underlying error (sentinel or wrapped), kept on the chain
//
// KernelError so errors.Is / errors.As can reach through.
type KernelError struct {
	Op      string
	Code    string
	TaskID  string
	AgentID string
	Err     error
}

// Error formats a single greppable line: kernel:<op> task=<id> agent=<id> <code>: <err>.
func (e *KernelError) Error() string {
	var b strings.Builder
	b.Grow(32 + len(e.Op) + len(e.Code) + len(e.TaskID) + len(e.AgentID))
	b.WriteString("kernel:")
	b.WriteString(e.Op)
	if e.TaskID != "" {
		b.WriteString(" task=")
		b.WriteString(e.TaskID)
	}
	if e.AgentID != "" {
		b.WriteString(" agent=")
		b.WriteString(e.AgentID)
	}
	b.WriteString(" ")
	b.WriteString(e.Code)
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

// Unwrap preserves the underlying error chain for errors.Is / errors.As.
func (e *KernelError) Unwrap() error { return e.Err }

// Kernel constructs a KernelError with a fixed op and code. The underlying
// Kernel error may be nil; taskID and agentID may be empty.
func Kernel(op, code, taskID, agentID string, err error) *KernelError {
	return &KernelError{Op: op, Code: code, TaskID: taskID, AgentID: agentID, Err: err}
}
