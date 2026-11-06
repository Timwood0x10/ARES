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
	"sync/atomic"

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
	_, err := s.AdmitFresh(ctx, sessionID, prompt)
	return err
}

// AdmitFresh admits the session and reports whether THIS call created it.
//
// The answer is computed inside the per-session admission lock, so two concurrent submissions of a
// new session cannot both believe they created it. Callers need the distinction to decide who owns
// the cleanup when the rest of the submission fails: releasing a session someone else is using
// would delete their live graph, while leaving one this call created orphans a READY root task.
func (s *Sessions) AdmitFresh(ctx context.Context, sessionID, prompt string) (bool, error) {
	if s == nil || sessionID == "" {
		return false, nil
	}
	unlock := s.lockAdmission(sessionID)
	defer unlock()
	return s.admitFreshLocked(ctx, sessionID, prompt)
}

// AdmitAndDo admits the session and runs do while the per-session admission lock is held.
//
// The lock has to span the caller's work, not just the admission: ownership of the cleanup depends
// on who created the session, and that fact is only useful if nobody can start using the session in
// between. Without it two submissions of one session can interleave so that the one that OWNS the
// session fails and tears it down, while the one that merely joined it is still creating its plan —
// destroying work that had already been accepted.
//
// do receives created so it can decide whether a failure means "roll back what I made" or "leave
// someone else's session alone". Holding the lock across a fabric Create is cheap (in-memory plus a
// deferred flush) and only contends with submissions for the same session.
func (s *Sessions) AdmitAndDo(ctx context.Context, sessionID, prompt string, do func(created bool) error) (bool, error) {
	if s == nil || sessionID == "" || do == nil {
		return false, nil
	}
	unlock := s.lockAdmission(sessionID)
	defer unlock()
	created, err := s.admitFreshLocked(ctx, sessionID, prompt)
	if err != nil {
		return false, err
	}
	return created, do(created)
}

// admitFreshLocked is AdmitFresh's body; the caller holds the per-session admission lock.
func (s *Sessions) admitFreshLocked(ctx context.Context, sessionID, prompt string) (bool, error) {
	if s == nil || sessionID == "" {
		return false, nil
	}
	if s.Reg == nil {
		return false, fmt.Errorf("agentruntime: cannot admit session %q without a session registry", sessionID)
	}
	// A "/" in the ID breaks the reaper keep-set (SessionIDFromNode
	// reverse-parses at the first slash), so "a/b" would let the reaper
	// harvest a LIVE session's history. Reject at the boundary.
	if strings.Contains(sessionID, "/") {
		return false, fmt.Errorf("agentruntime: session id %q must not contain a slash", sessionID)
	}
	if _, err := s.Reg.GetSession(sessionID); err == nil {
		return false, nil // already live: this call did not create it
	} else if !errors.Is(err, agentfabric.ErrSessionNotFound) {
		return false, fmt.Errorf("agentruntime: look up session %q: %w", sessionID, err)
	}
	if s.Compile == nil || s.Fabric == nil {
		return false, fmt.Errorf("agentruntime: cannot admit session %q without compile coordinator and fabric", sessionID)
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
				return false, nil
			}
		}
		return false, fmt.Errorf("agentruntime: init session %q: %w", sessionID, err)
	}

	// Compile the root task the planner's first quantum reads. An already
	// compiled root means a retried admission after a partial failure —
	// adopt it, but only while that root is still live (see below).
	rootStep := g.DAG().StepIndex()[g.Root()]
	if _, err := s.Fabric.CompileNode(liveCtx, planprojection.ProjectStep(rootStep)); err != nil {
		if !errors.Is(err, taskfabric.ErrTaskExists) {
			s.ReleaseQuietly(sessionID)
			return false, fmt.Errorf("agentruntime: compile session %q root: %w", sessionID, err)
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
				return false, fmt.Errorf("agentruntime: recompile session %q root: %w", sessionID, err)
			}
		}
	}
	slog.InfoContext(liveCtx, "agentruntime: admitted L2 session",
		"session_id", sessionID, "root", g.Root())
	return true, nil // created by this call
}

// SessionTopology reports what the session graph knows that the fabric does not yet.
//
// It exists because "the fabric holds no unfinished task" and "the session can produce nothing more"
// are different statements: between a graph growing a node and the projection compiling it into a
// task, the fabric is empty of pending work while the session is actively working. A stall verdict
// taken in that window is wrong, and it is wrong in the direction that loses the answer.
type SessionTopology interface {
	// PendingProjections reports how many of the session's graph nodes have no compiled task yet.
	PendingProjections(sessionID string) int
}

// PendingProjections implements SessionTopology over the session registry and the task fabric.
//
// A node without a task is exactly the gap: the graph has grown, the projection has not caught up.
func (s *Sessions) PendingProjections(sessionID string) int {
	if s == nil || s.Reg == nil || s.Fabric == nil || sessionID == "" {
		return 0
	}
	g, err := s.Reg.GetSession(sessionID)
	if err != nil {
		return 0
	}
	pending := 0
	for _, step := range g.DAG().StepIndex() {
		if step == nil {
			continue
		}
		if _, taskErr := s.Fabric.Task(step.ID); taskErr != nil {
			pending++
		}
	}
	return pending
}

// registryTopology adapts a bare registry + fabric to SessionTopology, for callers that hold the
// pieces but not a *Sessions (the HTTP task-read path does).
type registryTopology struct {
	reg    *agentfabric.SessionRegistry
	fabric *taskfabric.Fabric
}

// NewSessionTopology builds a SessionTopology from a registry and the fabric its nodes compile into.
func NewSessionTopology(reg *agentfabric.SessionRegistry, fabric *taskfabric.Fabric) SessionTopology {
	if reg == nil || fabric == nil {
		return nil
	}
	return registryTopology{reg: reg, fabric: fabric}
}

func (t registryTopology) PendingProjections(sessionID string) int {
	return (&Sessions{Reg: t.reg, Fabric: t.fabric}).PendingProjections(sessionID)
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
	return SessionStalledWith(fabric, sessionID, planTaskID, nil)
}

// SessionStalledWith is SessionStalled aware of the session graph.
//
// topo may be nil (legacy callers): the verdict then keeps its old meaning — "the fabric has no
// unfinished work" — which is only a proxy for "the session cannot answer".
func SessionStalledWith(fabric *taskfabric.Fabric, sessionID, planTaskID string, topo SessionTopology) bool {
	if fabric == nil || sessionID == "" {
		return false
	}
	if topo != nil && topo.PendingProjections(sessionID) > 0 {
		// The graph has nodes the fabric has not seen yet: the session is compiling, not stalled.
		return false
	}
	if planTaskID != "" {
		tk, err := fabric.Task(planTaskID)
		if err != nil || (!tk.CompletionSettled() && tk.State != taskfabric.StateFailed) {
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
		if !tk.CompletionSettled() && tk.State != taskfabric.StateFailed {
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
// that survives the answer-node compile gap. The zero value is ready to use.
// One detector still belongs to one wait loop; the counter is atomic purely
// as defensive hardening so an accidental concurrent Stalled call cannot
// data-race — atomicity does NOT make a shared detector meaningful (two
// loops polling different sessions through one detector would interleave
// each other's evidence).
type StallDetector struct {
	consecutive atomic.Int64
	// topo, when set, tells the detector what the graph knows beyond the fabric. Without it the
	// verdict can only see compiled work, which is empty while a projection is still pending.
	topo SessionTopology
}

// WithTopology makes the detector aware of the session graph.
//
// Chainable so existing construction sites (`&StallDetector{}`) keep working: a detector built
// without it behaves exactly as before, which is what the legacy tests assert.
func (d *StallDetector) WithTopology(topo SessionTopology) *StallDetector {
	if d != nil {
		d.topo = topo
	}
	return d
}

// Stalled reports whether the session should be treated as stalled now. A
// poll that sees any non-terminal session work resets the accumulated
// evidence, so on-again/off-again progress never adds up to a false verdict.
//
// Callers keep SessionStalled's own contract: run the answer scan FIRST, then
// ask the detector.
func (d *StallDetector) Stalled(fabric *taskfabric.Fabric, sessionID, planTaskID string) bool {
	if !SessionStalledWith(fabric, sessionID, planTaskID, d.topo) {
		d.consecutive.Store(0)
		return false
	}
	return d.consecutive.Add(1) >= StallConfirmations
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
