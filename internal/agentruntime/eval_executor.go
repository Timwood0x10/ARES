package agentruntime

import (
	"context"
	"fmt"
	"sync"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// SessionEvalExecutor runs one eval case through the REAL runtime: it submits a
// session, waits for the terminal answer, and reports where that answer's body
// came from (defect P0-3).
//
// It exists so the accuracy signal is a runtime FACT rather than a judge's guess
// about the answer's text: a gap body or a depth-capped answer is recorded by the
// planner as it happens, and no amount of well-written gap prose can change it.
// A judge that only reads text cannot tell "did less and lied about finishing"
// from "answered tersely" — this executor supplies the fact the deterministic
// floor needs.
//
// It satisfies the eval package's AgentExecutor and ProvenanceReporter
// STRUCTURALLY (both are one-method shapes) rather than by importing eval: the
// interface belongs to the consumer, and the runtime must not learn about the
// evaluation framework to be measurable by it.
//
// It does NOT drain tasks. The caller must already have a running scheduler — the
// same one serving the process — because this executor's job is to submit and
// observe, never to become a second execution loop (one production execution
// path).
//
// Concurrency: Execute is expected to be called sequentially (the eval runner
// walks a suite). AnswerProvenance reports the LAST case's facts under a lock, so
// concurrent use cannot corrupt them — but with concurrent Execute calls "last"
// would be whichever finished last, which is why the runner's sequential walk is
// the intended usage.
type SessionEvalExecutor struct {
	fabric    *taskfabric.Fabric
	submitter *Submitter
	sessions  *Sessions
	opts      AwaitOptions

	mu         sync.Mutex
	provenance TaskProvenance
}

// TaskProvenance is where an executed case's answer came from: the
// ares_events.AnswerBodySource* value the runtime recorded ("" when it recorded
// none — a non-answer task, or an envelope predating the keys) and whether the
// growth-depth guard forced the answer.
type TaskProvenance struct {
	// BodySource is one of the ares_events.AnswerBodySource* values, or "".
	BodySource string
	// Truncated reports a depth-guard-forced answer.
	Truncated bool
}

// NewSessionEvalExecutor builds the executor over an assembled execution core.
//
// It fails loud on a missing core: an executor that cannot actually run would
// turn every golden case into "no signal" — and "no signal" is scored as
// no-penalty, so the failure would look like a healthy result. That is the exact
// silent-success failure mode this path exists to remove.
func NewSessionEvalExecutor(exec *Execution, opts AwaitOptions) (*SessionEvalExecutor, error) {
	if exec == nil || exec.Submitter == nil || exec.Sessions == nil || exec.Sessions.Fabric == nil {
		return nil, fmt.Errorf("agentruntime: session eval executor requires an assembled execution core")
	}
	return &SessionEvalExecutor{
		fabric:    exec.Sessions.Fabric,
		submitter: exec.Submitter,
		sessions:  exec.Sessions,
		opts:      opts,
	}, nil
}

// Execute runs one case: submit the input as a session's plan task, wait for the
// terminal answer, and record the answer's provenance for AnswerProvenance.
//
// toolsUsed and tokensUsed are deliberately not reported (nil / 0): the accuracy
// path does not consume them, and inventing plausible numbers for them would be
// worse than reporting nothing.
func (e *SessionEvalExecutor) Execute(ctx context.Context, input string) (string, []string, int, error) {
	taskID, sessionID, err := e.submitter.Submit(ctx, PlanCapability, map[string]any{"input": input})
	if err != nil {
		return "", nil, 0, fmt.Errorf("submit case: %w", err)
	}
	// Release on EVERY exit path: a released session's terminal tasks become
	// reapable. The answer body releases on success, but a failure or a timeout
	// does not, and a long-lived eval loop must not pin them.
	defer e.sessions.ReleaseQuietly(sessionID)

	res := AwaitSessionResult(ctx, e.fabric, sessionID, taskID, e.opts)
	if res.Err != nil {
		return "", nil, 0, res.Err
	}

	bodySource, truncated, _ := SessionAnswerProvenance(e.fabric, sessionID)
	e.mu.Lock()
	e.provenance = TaskProvenance{BodySource: bodySource, Truncated: truncated}
	e.mu.Unlock()

	return res.Answer, nil, 0, nil
}

// AnswerProvenance reports the last executed case's provenance — the optional,
// narrowly-typed half of the executor contract that the eval runner discovers by
// type assertion.
func (e *SessionEvalExecutor) AnswerProvenance() (bodySource string, truncated bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.provenance.BodySource, e.provenance.Truncated
}
