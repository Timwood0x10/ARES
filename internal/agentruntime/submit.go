package agentruntime

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync/atomic"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// L2 capability constants re-exported so submission glue (cmd/ares, SDK)
// spells the single execution path without importing the agent fabric.
const (
	// PlanCapability is the submission capability in the single-L2-path
	// world (every submitted task is the first plan quantum of its session).
	PlanCapability = agentfabric.PlanCapability
	// AnswerCapability is the terminal L2 node: the session's sole exit.
	AnswerCapability = agentfabric.AnswerCapability
	// RootCapability is the capability of session admission roots.
	RootCapability = agentfabric.RootCapability
)

// Submitter owns the single user-submission path into the Task Fabric: every
// submission becomes an L2 session task (session-less payloads are
// auto-admitted into a fresh session; the capability is normalized to
// PlanCapability), and the process-local ID sequence that mints collision-free
// peer-plan-N / sess-auto-N identifiers lives HERE — one sequence per
// runtime, not a package global — so cmd/ares and the SDK share the same
// admission semantics through thin glue.
type Submitter struct {
	sessions *Sessions
	// seq is the monotonic ID sequence. Instance-scoped so two runtimes in
	// one process (tests, SDK + CLI embedding) never share counters.
	seq atomic.Int64
}

// NewSubmitter builds the submission path over a Sessions view.
func NewSubmitter(sessions *Sessions) *Submitter {
	return &Submitter{sessions: sessions}
}

// Seed advances the ID sequence to at least min — the cross-restart
// collision guard for peer-plan-N / sess-auto-N: with a durable store the
// restored fabric still holds the previous boot's IDs while the counter
// reset to 1, and a re-minted peer-plan-N fails Create with ErrTaskExists
// (a re-minted sess-auto-N silently re-admits onto a restored session's
// root task). Grow-only via CAS: a min below the current value is a
// no-op, so a fresh store (min 0) leaves the counter untouched and
// concurrent submissions can never move it backwards.
func (s *Submitter) Seed(min int64) {
	if s == nil {
		return
	}
	for {
		cur := s.seq.Load()
		if min <= cur || s.seq.CompareAndSwap(cur, min) {
			return
		}
	}
}

// MaxRestoredSeq extracts the highest counter value N embedded in any
// counter-derived task ID of a restored fabric. The ID families minted from
// process-local counters all end in "-N" before an optional "/" or "#"
// suffix:
//
//	peer-plan-N              (Submitter root tasks)
//	sess/sess-auto-N/d0/t#s  (session-scoped node tasks)
//	task-<capability>-N      (agentsyscall create_task)
//	plan-<origin>-N/rR#s     (agentsyscall create_plan rounds)
//
// A generic last-dash scan intentionally covers every such family (including
// future ones) instead of enumerating prefixes: over-seeding only skips ID
// values, while a missed family would let a fresh boot mint a colliding ID.
// Non-numeric or non-positive tails (UUIDs, engine step IDs) parse to 0 and
// are ignored.
func MaxRestoredSeq(ids []string) int64 {
	var maxN int64
	for _, id := range ids {
		dash := strings.LastIndexByte(id, '-')
		if dash < 0 || dash+1 >= len(id) {
			continue
		}
		tail := id[dash+1:]
		if cut := strings.IndexAny(tail, "/#"); cut >= 0 {
			tail = tail[:cut]
		}
		n, err := strconv.ParseInt(tail, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		if n > maxN {
			maxN = n
		}
	}
	return maxN
}

// Submit creates a task directly in the Task Fabric for the L2 session
// runtime (no leader dispatch): the task enters READY and the scheduler
// picks it up via the normal Schedule → Acquire → RunQuantum path.
//
// Single execution path. EVERY submission becomes an L2 session task:
//   - session-less payloads are auto-admitted into a fresh session (the
//     capability argument is normalized to PlanCapability with an info log);
//   - the envelope always carries SessionID, so the planner's first quantum
//     finds a live graph and no session-less legacy task can exist.
//
// There is no legacy path anymore — a submission that cannot be admitted
// fails fast instead of degrading into an unrunnable task.
//
// Returns the created root-task ID and the effective session ID (for
// release/wait by callers that drove the auto-admission).
func (s *Submitter) Submit(ctx context.Context, capability string, payload map[string]any) (taskID, sessionID string, err error) {
	if s == nil || s.sessions == nil || s.sessions.Fabric == nil {
		return "", "", fmt.Errorf("agentruntime: submit path not wired (nil sessions/fabric)")
	}
	// Copy the caller's payload: it is read-only input — the session
	// stamping below must not mutate a map the caller still holds.
	if payload == nil {
		payload = map[string]any{}
	} else {
		cp := make(map[string]any, len(payload)+1)
		for k, v := range payload {
			cp[k] = v
		}
		payload = cp
	}
	// Normalize every submission onto the L2 session path.
	sessionID, _ = payload["session_id"].(string)
	prompt, _ := payload["input"].(string)
	if sessionID == "" {
		sessionID = fmt.Sprintf("sess-auto-%d", s.seq.Add(1))
		payload["session_id"] = sessionID
	}
	if capability != PlanCapability {
		slog.InfoContext(ctx, "agentruntime: capability normalized to single L2 execution path",
			"from", capability, "to", PlanCapability, "session_id", sessionID)
		capability = PlanCapability
	}
	if err := s.sessions.Admit(ctx, sessionID, prompt); err != nil {
		return "", sessionID, err
	}
	taskID = fmt.Sprintf("peer-plan-%d", s.seq.Add(1))

	env := &taskfabric.CheckpointEnvelope{
		Payload: payload,
	}
	// SessionID is always stamped (auto-admitted above), so the
	// plannerCognition always finds a live per-session L2 graph.
	env.SessionID = sessionID
	task := &taskfabric.Task{
		ID:         taskID,
		Capability: capability,
		// Origin stays "" — this is a root task (user-submitted work), no
		// agent caller. Agent-created tasks get their Origin from the
		// create_task syscall's tool context.
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
		Checkpoint:  env,
	}
	if err := s.sessions.Fabric.Create(task); err != nil {
		return "", sessionID, fmt.Errorf("agentruntime: create task: %w", err)
	}
	slog.InfoContext(ctx, "agentruntime: submitted task → READY", "task_id", taskID, "capability", capability)
	return taskID, sessionID, nil
}
