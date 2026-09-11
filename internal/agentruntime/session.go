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

	"github.com/Timwood0x10/ares/internal/ares_events"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// Sessions owns the per-session L2 lifecycle over a task fabric. Both the
// serve and SDK entry points drive sessions through this type.
//
// All fields are injected at assembly and then read-only; Sessions holds no
// mutable state of its own, so it is safe for concurrent use.
type Sessions struct {
	// Reg is the per-session L2 graph registry (required).
	Reg *agentfabric.SessionRegistry
	// Fabric is the task fabric the session's nodes compile into (required).
	Fabric *taskfabric.Fabric
	// Compile is the incremental projection coordinator that turns graph
	// events into fabric tasks (required).
	Compile *planprojection.CompileCoordinator
}

// Admit registers one L2 session before its first task is created: it
// creates the session graph, subscribes it to the shared incremental
// compiler, and compiles the root task the planner's first quantum reads.
//
// Admission is idempotent: resubmitting into a live session is a multi-turn
// continuation, not an error — the existing session is reused and no
// duplicate root is compiled. Failures are fail-fast: anything InitSession
// registered before a failure is released again so a retry starts clean.
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
	if _, err := s.Reg.GetSession(sessionID); err == nil {
		return nil
	} else if !errors.Is(err, agentfabric.ErrSessionNotFound) {
		return fmt.Errorf("agentruntime: look up session %q: %w", sessionID, err)
	}
	if s.Compile == nil || s.Fabric == nil {
		return fmt.Errorf("agentruntime: cannot admit session %q without compile coordinator and fabric", sessionID)
	}

	// The compile subscription must outlive the submission request: tying it
	// to the request context would kill the projection the moment the caller
	// returns, while the session lives on.
	liveCtx := context.WithoutCancel(ctx)
	compile := s.Compile
	g, err := s.Reg.InitSession(liveCtx, sessionID, prompt, nil,
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
