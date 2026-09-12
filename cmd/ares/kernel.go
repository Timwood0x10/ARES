// kernel — merged CLI source: kernel.go, kernel_adopt.go, kernel_bridge.go,
// kernel_dispatcher.go, kernel_loop.go, scheduler_compat.go,
// runtime_bridge.go.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/agentsyscall"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/introspect"
	"github.com/Timwood0x10/ares/internal/kernel"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// kernelHandle carries the assembled kernel from agent construction to the
// serve wiring.
//
// The Kernel pillars (ares-runtime.md §13) are assembled here:
//   - fabric:   Scheduler pillar (taskfabric: Create/Schedule/Acquire/RunQuantum)
//   - agents:   Lifecycle pillar (agentfabric: spawn/suspend/resume/retire/kill)
//   - recovery: Lifecycle recovery surface (aresrecovery: lease-expiry requeue /
//     checkpoint resume / agent restart)
//   - dual/flag: IPC pillar (agentipc: single-track Task Fabric dispatch +
//     execution policy; the legacy leader track was removed)
type kernelHandle struct {
	dual *agentipc.DualTrackDispatcher
	flag *agentipc.PolicyFlag

	fabric    *taskfabric.Fabric
	agents    *agentfabric.Fabric
	recovery  *aresrecovery.Recovery
	executors map[string]CapabilityExecutor
	// scheduler is the running kernelScheduler. Retained so
	// wireKernelLifecycle can attach the governance provider once the agent
	// fabric exists (the scheduler may start before the lifecycle wiring).
	scheduler *kernelScheduler
	// intro serves the runtime introspection panel (monitoring.md). Wired in
	// createAndServeAgents when the full kernel exists; nil on partial paths.
	intro *introspect.Handler
	// tracker is the shared per-agent load/confidence/priority source for the
	// scheduler and the fabric dispatch path. It is created at startup and
	// retained so agent priorities can be injected into it (OS-thread-style
	// thread priority).
	tracker *loadTracker
	// peerRegistry is the direct peer-to-peer messaging discovery surface
	// (primitive 2). Retained so the registry built at serve time stays
	// reachable for agent messaging / capability discovery instead of being
	// discarded (the peer registry return value was previously dropped).
	peerRegistry *peer.Registry
	// syscalls is the agentsyscall.Kernel backing spawn_agent/create_task/
	// ask_agent. Retained so the collaboration IPC bridge (built later in
	// setupPeerRegistry) can inject ipc.Send into ask_agent.
	// Nil on partial paths without syscalls.
	syscalls *agentsyscall.Kernel
	// compileCoord is the projection coordinator. It projects the live
	// MutableDAG into taskfabric PlanSteps and records compile provenance
	// for introspection. Nil when no live DAG is wired.
	compileCoord *planprojection.CompileCoordinator
	// sessionReg is the per-session L2 graph registry. Non-nil only
	// when the DAG execution gate is open; submitPeerTask admits sessions
	// through it. Nil = legacy path, session payloads stay envelope-only.
	sessionReg *agentfabric.SessionRegistry
	// submitter is the shared L2 submission path (agentruntime.Submitter):
	// session admission, root-task creation and the process-local ID
	// sequence with its cross-restart seed. Nil until the peer kernel is
	// assembled.
	submitter *agentruntime.Submitter
	// pluginBus is the runtime plugin ecosystem hooked to the scheduler's
	// quantum boundary (runtime_bridge.go). Nil when the scheduler is absent.
	pluginBus *runtime.PluginBus
	// schedulerStop / schedulerDone drive the scheduler drain loop's managed
	// teardown: Stop cancels the loop context, Wait joins the goroutine.
	// Nil on partial paths — the adopt adapter skips those hooks.
	schedulerStop context.CancelFunc
	schedulerDone chan struct{}
	// recoveryStop / recoveryDone do the same for the kernel recovery loop.
	recoveryStop context.CancelFunc
	recoveryDone chan struct{}
	flipped      bool
}

// sessions builds the shared per-session L2 lifecycle helper over this
// kernel's registry/fabric/compiler. Sessions is a read-only view (no state
// of its own), so allocating it per use is fine.
func (k *kernelHandle) sessions() *agentruntime.Sessions {
	if k == nil {
		return &agentruntime.Sessions{}
	}
	return &agentruntime.Sessions{Reg: k.sessionReg, Fabric: k.fabric, Compile: k.compileCoord}
}

// System Runtime registry names of the six kernel pillars. The
// dependency edges below turn these names into the shutdown order
// pluginbus/recovery → scheduler → dispatcher → fabrics → eventstore.
const (
	sysCompScheduler   = "scheduler"
	sysCompTaskFabric  = "taskfabric"
	sysCompAgentFabric = "agentfabric"
	sysCompRecovery    = "recovery"
	sysCompDispatcher  = "dispatcher"
	sysCompPluginBus   = "pluginbus"
)

// adoptReadyPollInterval is the polling cadence of the scheduler readiness
// gate; adoptReadyPollBudget bounds the total wait.
const (
	adoptReadyPollInterval = 50 * time.Millisecond
	adoptReadyPollBudget   = 2 * time.Second
)

// kernelComponent adapts one kernel pillar to the System Runtime Component
// contract. Identity + dependency metadata drive the registry ordering;
// optional ready/stop/wait hooks let Adopt verify real readiness (the
// scheduler must be draining to count as Ready) and let Shutdown drive real
// teardown (cancel + wait the loop's goroutine). Nil hooks are safe
// no-ops, so passive pillars (the fabrics, the dispatcher — pure in-memory
// state machines with no goroutine of their own) still join the graph and
// the shutdown order.
type kernelComponent struct {
	name    string
	deps    []string
	mode    kernel.Mode
	readyFn func(ctx context.Context) error
	stopFn  func(ctx context.Context) error
	waitFn  func() error
}

// Name returns the stable component identifier.
func (a *kernelComponent) Name() string { return a.name }

// Dependencies returns the names of components that must exist (and not be
// Failed) before this one is adopted; they also decide the shutdown order.
func (a *kernelComponent) Dependencies() []string { return a.deps }

// Ready delegates to the optional readiness hook; nil means Ready by
// construction.
func (a *kernelComponent) Ready(ctx context.Context) error {
	if a.readyFn == nil {
		return nil
	}
	return a.readyFn(ctx)
}

// Stop delegates to the optional teardown hook; nil is a no-op.
func (a *kernelComponent) Stop(ctx context.Context) error {
	if a.stopFn == nil {
		return nil
	}
	return a.stopFn(ctx)
}

// Wait delegates to the optional wait hook; nil is a no-op.
func (a *kernelComponent) Wait() error {
	if a.waitFn == nil {
		return nil
	}
	return a.waitFn()
}

// schedulerReady verifies the drain loop is actually running: it polls
// Scheduler.Running with a bounded budget so the natural delay between
// `go sched.Run(...)` and adoption does not produce a false Degraded, while
// a genuinely dead loop still reports a readable reason instead of Ready.
func schedulerReady(sched *kernelScheduler) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if sched.Running() {
			return nil
		}
		deadline := time.Now().Add(adoptReadyPollBudget)
		ticker := time.NewTicker(adoptReadyPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return fmt.Errorf("scheduler drain loop not running (readiness check aborted: %v)", ctx.Err())
			case <-ticker.C:
				if sched.Running() {
					return nil
				}
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("scheduler drain loop not running after %s", adoptReadyPollBudget)
			}
		}
	}
}

// adopt registers the six kernel pillars with the System Runtime orchestrator:
// the kernel pillars are assembled LATER than
// the Bootstrap components, so they join the orchestrator through
// Orchestrator.Adopt instead of the startup-time Register. The adopt path
// owns the component names, the dependency edges (which decide the
// reverse-topological shutdown order), the stop/wait hooks, and the unified
// background-loop entry (runBackground).
//
// Nil pillars are skipped (present semantics, mirroring
// registerSystemComponent): partial kernels (SDK-adjacent paths, tests) keep
// working. A non-nil error from Adopt fails the serve startup loudly — a
// kernel pillar that cannot join the managed graph would otherwise recreate
// the "false Ready" blind spot.
func (k *kernelHandle) adopt(ctx context.Context, orch *kernel.Orchestrator) error {
	if k == nil {
		return nil
	}
	if orch == nil {
		log.Info("serve: system runtime not wired; kernel components not adopted (unmanaged lifecycle)")
		return nil
	}

	// eventstore is the Bootstrap-registered dependency edge for both
	// fabrics; it decides that the fabrics stop BEFORE the store.
	eventstore := ares_bootstrap.SysCompEventStore

	components := []*kernelComponent{
		{
			// Passive state machine — no goroutine, nothing to stop. Its
			// presence in the graph orders the dispatcher/scheduler before
			// the event store at shutdown.
			name: sysCompTaskFabric,
			deps: []string{eventstore},
		},
		{
			// Passive population registry — same passive-stop semantics.
			name: sysCompAgentFabric,
			deps: []string{eventstore},
		},
		{
			// Dispatch is synchronous through the fabrics; stopping the
			// fabrics first (reverse topo) already halts new dispatch.
			name: sysCompDispatcher,
			deps: []string{sysCompTaskFabric, sysCompAgentFabric},
		},
		{
			// The scheduler owns the drain loop goroutine: Stop cancels its
			// context, Wait joins it. Degraded mode: a loop that is not
			// running reports Degraded + reason, never a false Ready.
			name: sysCompScheduler,
			deps: []string{sysCompTaskFabric, sysCompAgentFabric, sysCompDispatcher},
			mode: kernel.ModeDegraded,
			readyFn: func(ctx context.Context) error {
				if k.scheduler == nil {
					return nil
				}
				return schedulerReady(k.scheduler)(ctx)
			},
			stopFn: func(ctx context.Context) error {
				if k.schedulerStop != nil {
					k.schedulerStop()
				}
				return nil
			},
			waitFn: func() error {
				if k.schedulerDone != nil {
					<-k.schedulerDone
				}
				return nil
			},
		},
		{
			// Recovery owns the requeue/restart loop goroutine.
			name: sysCompRecovery,
			deps: []string{sysCompTaskFabric, sysCompAgentFabric},
			stopFn: func(ctx context.Context) error {
				if k.recoveryStop != nil {
					k.recoveryStop()
				}
				return nil
			},
			waitFn: func() error {
				if k.recoveryDone != nil {
					<-k.recoveryDone
				}
				return nil
			},
		},
		{
			// PluginBus has a real Stop (plugin reverse-order teardown).
			name: sysCompPluginBus,
			deps: []string{sysCompScheduler},
			stopFn: func(ctx context.Context) error {
				if k.pluginBus == nil {
					return nil
				}
				return k.pluginBus.Stop(ctx)
			},
		},
	}

	for _, c := range components {
		if !k.componentPresent(c.name) {
			continue
		}
		mode := c.mode
		if mode == 0 {
			mode = kernel.ModeRequired
		}
		if err := orch.Adopt(ctx, c, mode); err != nil {
			return fmt.Errorf("serve: adopt kernel component %q: %w", c.name, err)
		}
	}
	log.Info("serve: kernel components adopted into system runtime (scheduler/taskfabric/agentfabric/recovery/dispatcher/pluginbus)")
	return nil
}

// componentPresent reports whether the named pillar exists on this kernel
// handle (present semantics: nil pillars are skipped, not an error).
func (k *kernelHandle) componentPresent(name string) bool {
	switch name {
	case sysCompTaskFabric:
		return k.fabric != nil
	case sysCompAgentFabric:
		return k.agents != nil
	case sysCompDispatcher:
		return k.dual != nil
	case sysCompScheduler:
		return k.scheduler != nil
	case sysCompRecovery:
		return k.recovery != nil
	case sysCompPluginBus:
		return k.pluginBus != nil
	default:
		return false
	}
}

// runBackground starts a managed background loop (no bare `go` on the
// serve path). With a wired System Runtime the loop joins the orchestrator's
// errgroup under the given name — a panic marks the component Failed and is
// recorded on the event sink — otherwise it falls back to the Bootstrap
// errgroup (same recover guarantees, no component marking).
//
// The fn receives the effective loop context: the orchestrator's managed
// root context on the adopted path, the caller's ctx on the fallback path.
// comp must be non-nil (every serve-path caller holds the Bootstrap
// container); a nil comp skips the loop loudly instead of leaking an
// unmanaged goroutine.
func runBackground(ctx context.Context, comp *ares_bootstrap.Components, name string, fn func(ctx context.Context) error) {
	if comp == nil {
		log.Info("serve: background loop skipped (no component container)", "name", name)
		return
	}
	if comp.SystemRuntime != nil {
		comp.SystemRuntime.GoBackground(name, fn)
		return
	}
	comp.GoBackground(ctx, name, fn)
}

// CapabilityExecutor is the scheduler's executor contract, aliased from the
// shared package so the whole cmd/ares codebase and the kernel loops use the
// identical interface.
type CapabilityExecutor = kernel.CapabilityExecutor

// kernelScheduler aliases the shared Scheduler, preserving cmd/ares's
// historical naming throughout kernel.go / agent.go / serve.go and their
// tests.
type kernelScheduler = kernel.Scheduler

// loadTracker aliases the shared per-agent load/confidence tracker.
type loadTracker = kernel.LoadTracker

// NewKernelScheduler creates the shared scheduler over a fabric. It mirrors
// the historical cmd/ares constructor signature; the implementation lives in
// kernel.New.
func NewKernelScheduler(fabric *taskfabric.Fabric, executors map[string]CapabilityExecutor, tracker *loadTracker) *kernelScheduler {
	return kernel.New(fabric, executors, tracker)
}

// newLoadTracker creates a shared tracker.
func newLoadTracker() *loadTracker {
	return kernel.NewLoadTracker()
}
