// Package agentruntime provides the shared L2 execution-kernel assembly used
// by both cmd/ares (serve) and sdk: the session registry, the incremental
// compile coordinator, the planner/router cognition, and the per-session task
// lifecycle (admission, release, harvest, reaping). It is the single source
// of the "one mainline" agent execution model — entry points differ only in
// their surface (HTTP vs SDK), never in how an agent runs.
//
// The package is deliberately surface-free: it never imports cmd/ or sdk/,
// and carries no HTTP/CLI concerns. Callers own transport, recovery, chaos and
// evolution; this package owns how a session is admitted, compiled, executed
// as L2 quanta, and reclaimed.
package agentruntime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_events"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// Sessions owns the per-session L2 lifecycle over a task fabric. Both the
// serve and SDK entry points drive sessions through this type.
//
// Reg/Fabric/Compile are injected at assembly and then read-only. The only
// mutable state is the per-session admission lock table (admitMu/admitLocks),
// which serializes Admit for one session ID — see lockAdmission.
type Sessions struct {
	// Reg is the per-session L2 graph registry (required).
	Reg *agentfabric.SessionRegistry
	// Fabric is the task fabric the session's nodes compile into (required).
	Fabric *taskfabric.Fabric
	// Compile is the incremental projection coordinator that turns graph
	// events into fabric tasks (required).
	Compile *planprojection.CompileCoordinator

	// admitMu guards admitLocks. admitLocks is the per-session admission
	// serialization table: without it two concurrent Submits into the same
	// RELEASED session both miss GetSession, one wins InitSession, and the
	// loser's ErrSessionAlreadyExists branch returned BEFORE the winner ran
	// the stale-task harvest — its wait loop then scanned the previous
	// turn's still-present COMPLETED answer and returned it as this turn's
	// result. Entries are refcounted so the table does not leak one entry
	// per session ever admitted.
	admitMu    sync.Mutex
	admitLocks map[string]*admitLock
}

// admitLock is one session's admission mutex plus its waiter count.
type admitLock struct {
	mu   sync.Mutex
	refs int
}

// lockAdmission serializes admission for one session ID. The returned
// unlock must be called exactly once. Concurrent admitters of DIFFERENT
// sessions never contend — only same-session admission is serialized, which
// is the only case where the harvest/recompile sequence may not interleave.
func (s *Sessions) lockAdmission(sessionID string) (unlock func()) {
	s.admitMu.Lock()
	if s.admitLocks == nil {
		s.admitLocks = make(map[string]*admitLock)
	}
	l := s.admitLocks[sessionID]
	if l == nil {
		l = &admitLock{}
		s.admitLocks[sessionID] = l
	}
	l.refs++
	s.admitMu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.admitMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.admitLocks, sessionID)
		}
		s.admitMu.Unlock()
	}
}

// Admit registers one L2 session before its first task is created: it
// creates the session graph, subscribes it to the shared incremental
// compiler, and compiles the root task the planner's first quantum reads.
//
// Admission is idempotent: resubmitting into a live session is a multi-turn
// continuation, not an error — the existing session is reused and no
// duplicate root is compiled. Failures are fail-fast: anything InitSession
// registered before a failure is released again so a retry starts clean.
//
// Admission for one session ID is serialized (lockAdmission): a concurrent
// re-admitter blocks until the winner has finished harvesting the previous
// turn's terminal tasks, so no caller ever returns from Admit into a fabric
// that still holds a stale answer its wait loop could mistake for a result.
func (s *Sessions) Admit(ctx context.Context, sessionID, prompt string) error {
	if s == nil || sessionID == "" {
		return nil
	}
	if s.Reg == nil {
		return fmt.Errorf("agentruntime: cannot admit session %q without a session registry", sessionID)
	}
	// A "/" in the ID breaks the reaper keep-set (SessionIDFromNode
	// reverse-parses at the first slash), so "a/b" would let the reaper
	// harvest a LIVE session's history. Reject at the boundary.
	if strings.Contains(sessionID, "/") {
		return fmt.Errorf("agentruntime: session id %q must not contain a slash", sessionID)
	}
	unlock := s.lockAdmission(sessionID)
	defer unlock()
	if _, err := s.Reg.GetSession(sessionID); err == nil {
		return nil
	} else if !errors.Is(err, agentfabric.ErrSessionNotFound) {
		return fmt.Errorf("agentruntime: look up session %q: %w", sessionID, err)
	}
	if s.Compile == nil || s.Fabric == nil {
		return fmt.Errorf("agentruntime: cannot admit session %q without compile coordinator and fabric", sessionID)
	}

	// InitSession wires the compile subscription on a context the registry
	// owns (released via ReleaseSession/idle sweep), so no caller-scoped
	// workaround is needed here. liveCtx still shields the ROOT COMPILATION
	// below from the submission request ending mid-admission.
	liveCtx := context.WithoutCancel(ctx)
	compile := s.Compile
	g, err := s.Reg.InitSession(sessionID, prompt, nil,
		func(subCtx context.Context, dag *engine.MutableDAG) (stop func()) {
			return compile.SubscribeGraphEvents(subCtx, dag)
		})
	if err != nil {
		// A concurrent admitter may have won the race between our GetSession
		// and InitSession — re-check before failing.
		if errors.Is(err, agentfabric.ErrSessionAlreadyExists) {
			if _, err2 := s.Reg.GetSession(sessionID); err2 == nil {
				return nil
			}
		}
		return fmt.Errorf("agentruntime: init session %q: %w", sessionID, err)
	}

	// Compile the root task the planner's first quantum reads. An already
	// compiled root means a retried admission after a partial failure —
	// adopt it, but only while that root is still live (see below).
	rootStep := g.DAG().StepIndex()[g.Root()]
	if _, err := s.Fabric.CompileNode(liveCtx, planprojection.ProjectStep(rootStep)); err != nil {
		if !errors.Is(err, taskfabric.ErrTaskExists) {
			s.ReleaseQuietly(sessionID)
			return fmt.Errorf("agentruntime: compile session %q root: %w", sessionID, err)
		}
		// An existing TERMINAL root belongs to a previous session that
		// already released under this same ID (client "continue the chat"),
		// not to this retry. Adopting it would hand the new turn the old
		// prompt and let stale same-named node tasks resolve as fresh
		// results. Harvest the stale tasks (the reaper's job, done early)
		// and recompile clean.
		if stale, terr := s.Fabric.Task(g.Root()); terr == nil &&
			(stale.State == taskfabric.StateCompleted || stale.State == taskfabric.StateFailed) {
			n := Harvest(s.Fabric, sessionID)
			slog.InfoContext(liveCtx, "agentruntime: session re-admitted after release, harvested stale tasks before recompiling root",
				"session_id", sessionID, "harvested", n)
			if _, err := s.Fabric.CompileNode(liveCtx, planprojection.ProjectStep(rootStep)); err != nil {
				s.ReleaseQuietly(sessionID)
				return fmt.Errorf("agentruntime: recompile session %q root: %w", sessionID, err)
			}
		}
	}
	slog.InfoContext(liveCtx, "agentruntime: admitted L2 session",
		"session_id", sessionID, "root", g.Root())
	return nil
}

// SessionStalled reports whether a session can no longer produce an answer:
// the submission's plan task is terminal, the session owns at least one
// fabric task, and every one of them is terminal too.
//
// It exists because grown L2 nodes carry MaxRetries=0 (planprojection), so a
// single non-first-quantum error is terminally FAILED; with the dependency
// cascade the continuation plan node dies with its failed tool and the answer
// node is never grown. The wait loops' fast-fail only checked the submission
// root and answer# tasks, so they spun their full deadline (10min SDK /
// collabTimeout serve) on a session that could never answer. A stalled
// session must fail fast instead.
//
// planTaskID is the submission root (peer-plan-N) — outside the sess/ prefix,
// and still LIVE while its plan quantum runs: a completed root alone says
// nothing, because the quantum that grows the session's nodes executes on the
// plan task. A non-terminal plan task therefore never stalls.
//
// Callers must run their answer scan FIRST: a completed answer means the
// session is finished, not stalled, and the scan is what returns its content.
func SessionStalled(fabric *taskfabric.Fabric, sessionID, planTaskID string) bool {
	if fabric == nil || sessionID == "" {
		return false
	}
	if planTaskID != "" {
		tk, err := fabric.Task(planTaskID)
		if err != nil || (tk.State != taskfabric.StateCompleted && tk.State != taskfabric.StateFailed) {
			// Plan quantum still in flight (or unreadable): the session may
			// yet grow nodes.
			return false
		}
	}
	prefix := agentfabric.SessionTaskPrefix(sessionID)
	seen := false
	for _, id := range fabric.IDs() {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		tk, err := fabric.Task(id)
		if err != nil {
			continue
		}
		seen = true
		if tk.State != taskfabric.StateCompleted && tk.State != taskfabric.StateFailed {
			return false
		}
	}
	return seen
}

// StallConfirmations is how many consecutive polls a wait loop must observe
// SessionStalled before it believes the verdict.
//
// The planner grows the answer node through the SAME asynchronous compile
// pipeline as a tool node (planner_cognition.growAnswerNode → L2Graph.
// AddToolNode → GraphEvent → CompileCoordinator → fabric.CompileNode), so a
// plan quantum can return Done — plan task COMPLETED — while the grown answer
// task is not in the fabric yet. A single poll landing in that gap saw "every
// session task terminal, no answer" and declared a live session dead, which
// is what made the SDK's session-continuation path fail intermittently under
// load. Requiring the verdict to be stable across polls absorbs the gap; a
// genuinely stalled session still reports within a few polls, far inside any
// caller's wait budget.
const StallConfirmations = 5

// StallDetector turns SessionStalled's point-in-time verdict into a decision
// that survives the answer-node compile gap. The zero value is ready to use;
// one detector belongs to one wait loop, and that loop must be the only
// goroutine touching it (no lock — same ownership rule as a loop-local
// counter).
type StallDetector struct {
	consecutive int
}

// Stalled reports whether the session should be treated as stalled now. A
// poll that sees any non-terminal session work resets the accumulated
// evidence, so on-again/off-again progress never adds up to a false verdict.
//
// Callers keep SessionStalled's own contract: run the answer scan FIRST, then
// ask the detector.
func (d *StallDetector) Stalled(fabric *taskfabric.Fabric, sessionID, planTaskID string) bool {
	if !SessionStalled(fabric, sessionID, planTaskID) {
		d.consecutive = 0
		return false
	}
	d.consecutive++
	return d.consecutive >= StallConfirmations
}

// Release drops a session. A release miss is returned to the caller so it can
// decide whether it matters (admission cleanup treats it as best-effort).
func (s *Sessions) Release(sessionID string) error {
	if s == nil || s.Reg == nil {
		return nil
	}
	return s.Reg.ReleaseSession(sessionID)
}

// ReleaseQuietly drops a half-admitted session during failure cleanup. The
// release is best-effort: admission already failed, and a release miss only
// leaves a normal session behind for the reaper.
func (s *Sessions) ReleaseQuietly(sessionID string) {
	_ = s.Release(sessionID)
}

// Harvest deletes every harvestable task under a released session's ID
// prefix: terminal (COMPLETED/FAILED) and READY tasks go; in-flight ones
// (LEASED/RUNNING/SUSPENDED) are refused by Delete and left for the reaper.
// Returns the number of tasks removed.
func Harvest(fabric *taskfabric.Fabric, sessionID string) int {
	if fabric == nil {
		return 0
	}
	prefix := agentfabric.SessionTaskPrefix(sessionID)
	removed := 0
	for _, id := range fabric.IDs() {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if fabric.Delete(id) == nil {
			removed++
		}
	}
	return removed
}

// KeepSet builds the reaper's keep predicate from the session registry: a
// task is kept while its owning session is still live. The registry is the
// single authority — an ID that parses as a session task but has no live
// session is harvestable once the grace window passes.
func KeepSet(reg *agentfabric.SessionRegistry) func(taskID string) bool {
	return func(taskID string) bool {
		if reg == nil {
			return false
		}
		sid, ok := agentfabric.SessionIDFromNode(taskID)
		if !ok {
			return false
		}
		_, err := reg.GetSession(sid)
		return err == nil
	}
}

// ReleaseOnAnswerFailure releases a session whose terminal answer task
// FAILED. Only the FAILED state releases: fabric.Fail's requeue branch also
// records task.failed (state READY) while the retry budget stands. Only the
// answer node releases here: it is the session's sole terminal exit. A
// release miss is logged, not an error — the postcondition (no live session)
// already holds.
func ReleaseOnAnswerFailure(ctx context.Context, reg *agentfabric.SessionRegistry, ev *ares_events.Event) {
	if ev == nil || reg == nil {
		return
	}
	if c, _ := ev.Payload["capability"].(string); c != agentfabric.AnswerCapability {
		return
	}
	if s, _ := ev.Payload["state"].(string); taskfabric.TaskState(s) != taskfabric.StateFailed {
		return
	}
	sid, _ := ev.Payload["session_id"].(string)
	if strings.TrimSpace(sid) == "" {
		return
	}
	if err := reg.ReleaseSession(sid); err != nil {
		slog.WarnContext(ctx, "agentruntime: answer-failure release found no live session",
			"session", sid, "error", err)
		return
	}
	slog.InfoContext(ctx, "agentruntime: released session after terminal answer failure",
		"session", sid, "task_id", ev.StreamID)
}
