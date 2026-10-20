package agentruntime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Timwood0x10/ares/internal/agents"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/kernel"
)

// FixedStrategySource serves ONE strategy: the candidate under evaluation.
//
// The runtime reads its strategy through this single-method interface (the planner
// cognition calls GetActiveStrategy), so substituting the source is what makes "run
// THIS candidate for real" possible without teaching the runtime about evolution: as
// far as that runtime knows, the candidate's prompt IS the active strategy.
//
// This is the piece that makes the alternative WRONG rather than merely incomplete:
// pointing a real executor at the unmodified runtime would run every candidate under
// the strategy that is currently promoted, so all candidates would score identically
// and the comparison would look stable while measuring nothing.
//
// A nil Strategy is served as (nil, nil) — the interface's documented "steer nothing"
// contract — so a candidate without a prompt template behaves like an unconfigured
// deployment instead of erroring.
type FixedStrategySource struct {
	Strategy *agents.ActiveStrategy
}

// GetActiveStrategy implements agents.StrategySource.
func (s FixedStrategySource) GetActiveStrategy(context.Context) (*agents.ActiveStrategy, error) {
	return s.Strategy, nil
}

var _ agents.StrategySource = FixedStrategySource{}

// CandidateRuntimeConfig is everything one candidate's isolated runtime needs.
//
// It carries an executor FACTORY rather than a prepared map on purpose: a capability
// executor runs quanta against one specific task fabric, and that fabric is created
// by the runtime (see NewCandidateRuntime). Accepting executors built for the serving
// fabric would dispatch one runtime's tasks into another runtime's execution bodies,
// which is exactly the isolation this type exists to provide.
type CandidateRuntimeConfig struct {
	// ChatClient is the model client the candidate's agents talk to (required).
	ChatClient agentfabric.ChatClient
	// ToolBinder advertises tool schemas to the candidate's planner (required).
	ToolBinder agentfabric.ToolBinder
	// Executors builds the capability executors over the core and the fabrics this
	// runtime just created; nil means DefaultCandidateExecutors (one plan-capable body
	// over the core's router). It is the caller's half of the assembly: WHAT runs a
	// quantum is a product decision (real tools, sandboxed tools, extra capabilities),
	// while HOW the evaluation environment is fenced off is this type's half.
	//
	// It receives the assembled core because an executor is a thin adapter over the
	// core's ROUTER — which is exactly why the core is built before this factory runs.
	// Returning an empty map is still refused: a runtime with no execution body would
	// stall every case instead of reporting the misconfiguration.
	Executors func(exec *Execution, fabric *taskfabric.Fabric, agents *agentfabric.Fabric) (map[string]kernel.CapabilityExecutor, error)
	// L1DAG is the optional L1 capability graph the planner reads before growing tool
	// nodes (nil = permissive, matching the SDK's posture).
	L1DAG *engine.MutableDAG
	// MaxPlanDepth caps L2 growth (0 = planner default).
	MaxPlanDepth int
	// Await bounds how long one case waits for its terminal answer.
	Await AwaitOptions
	// ToolCeiling is the tool whitelist the candidate may NOT exceed (nil =
	// EvaluationToolCeiling). A tighter list is a legitimate choice; a wider one should
	// be a deliberate edit to that exported default, because widening it here trades the
	// comparability of the whole comparison for realism in one candidate's run.
	ToolCeiling []string
	// Logger is shared by the cognition bodies (nil = package default).
	Logger *slog.Logger
}

// CandidateRuntime is one candidate's isolated execution environment: its own task
// and agent fabrics, its own scheduler loop, and an executor that runs evaluation
// cases through the real runtime with the candidate's prompt.
//
// Isolation is the whole point. Evaluation traffic must not become production traffic:
// cases submitted into the serving fabric would be picked up by the serving scheduler,
// counted against agent quotas, and left behind in the production task graph — and a
// candidate's failures would surface as production failures. Nothing here is shared.
type CandidateRuntime struct {
	executor *SessionEvalExecutor
	fabric   *taskfabric.Fabric
	sched    *kernel.Scheduler
	tools    []string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	closed bool
}

// NewCandidateRuntime assembles an isolated runtime for one candidate.
//
// It fails LOUD on a missing piece. A runtime that cannot actually run would turn
// every case into "no signal", and no signal is scored as no penalty, so the failure
// would read as a healthy candidate — the same silent-success shape
// NewSessionEvalExecutor refuses (this path exists to remove it, not to reintroduce it
// one layer up).
func NewCandidateRuntime(cfg CandidateRuntimeConfig, candidate *agents.ActiveStrategy) (*CandidateRuntime, error) {
	switch {
	case cfg.ChatClient == nil:
		return nil, fmt.Errorf("agentruntime: candidate runtime requires a chat client")
	case cfg.ToolBinder == nil:
		return nil, fmt.Errorf("agentruntime: candidate runtime requires a tool binder")
	}

	fabric := taskfabric.NewFabric()
	agentFabric := agentfabric.NewFabric()

	// The tool boundary is installed HERE rather than left to the caller: a fence the
	// caller has to remember is one forgotten call away from running candidates with the
	// full production tool surface.
	ceiling := cfg.ToolCeiling
	if len(ceiling) == 0 {
		ceiling = EvaluationToolCeiling
	}
	sandboxed, err := SandboxCandidateTools(candidate, ceiling)
	if err != nil {
		return nil, err
	}

	// Order matters: the core is assembled FIRST because the executors are thin adapters
	// over its router, and it cannot be assembled over executors that do not exist yet.
	exec, err := NewExecution(ExecutionConfig{
		Fabric:         fabric,
		Agents:         agentFabric,
		ChatClient:     cfg.ChatClient,
		ToolBinder:     cfg.ToolBinder,
		StrategySource: FixedStrategySource{Strategy: sandboxed},
		L1DAG:          cfg.L1DAG,
		MaxPlanDepth:   cfg.MaxPlanDepth,
		Logger:         cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("agentruntime: assemble candidate execution core: %w", err)
	}

	buildExecutors := cfg.Executors
	if buildExecutors == nil {
		buildExecutors = func(core *Execution, _ *taskfabric.Fabric, _ *agentfabric.Fabric) (map[string]kernel.CapabilityExecutor, error) {
			return DefaultCandidateExecutors(core)
		}
	}
	executors, err := buildExecutors(exec, fabric, agentFabric)
	if err != nil {
		return nil, fmt.Errorf("agentruntime: build candidate executors: %w", err)
	}
	if len(executors) == 0 {
		return nil, fmt.Errorf("agentruntime: candidate executor factory returned no executors — no case could run")
	}

	evalExecutor, err := NewSessionEvalExecutor(exec, cfg.Await)
	if err != nil {
		return nil, fmt.Errorf("agentruntime: candidate eval executor: %w", err)
	}

	return &CandidateRuntime{
		executor: evalExecutor,
		fabric:   fabric,
		sched:    kernel.New(fabric, executors, kernel.NewLoadTracker()),
		tools:    agents.ToolNamesFromParams(sandboxed.Params),
	}, nil
}

// Executor returns the case runner bound to this candidate's runtime.
//
// It satisfies eval.AgentExecutor structurally, so an evaluation runner can walk a
// suite through it without either side importing the other.
func (r *CandidateRuntime) Executor() *SessionEvalExecutor { return r.executor }

// Tools returns the tool whitelist actually installed for this candidate (the sandbox
// intersection, never what the candidate asked for).
//
// It is exported so the caller can LOG it at startup: "this candidate ran with these
// tools" is the fact an operator needs to interpret a score, and a fence nobody can see
// is indistinguishable from no fence.
func (r *CandidateRuntime) Tools() []string { return append([]string(nil), r.tools...) }

// Start runs this runtime's scheduler loop until ctx is cancelled or Close is called.
//
// The loop is this runtime's OWN: its cases are drained by it and by nothing else,
// which is what keeps evaluation traffic out of the serving scheduler. Start is not
// called by NewCandidateRuntime because the caller decides when the environment is
// ready (and must be able to Close a runtime it never started).
func (r *CandidateRuntime) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil || r.closed {
		// Already running, or closed for good: a second loop over one fabric would be
		// two schedulers draining the same tasks, and starting after Close would run a
		// loop nobody can stop.
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	go func() {
		defer close(r.done)
		r.sched.Run(loopCtx)
	}()
}

// Close stops the loop and waits for it to finish; it is safe to call more than once,
// and it permanently closes the runtime (a later Start is refused).
//
// Waiting is not optional: a caller that evaluates one candidate and moves to the next
// would otherwise leave a scheduler goroutine behind per candidate, and those goroutines
// hold the fabrics this type created.
func (r *CandidateRuntime) Close() {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.closed = true
	r.mu.Unlock()

	if cancel == nil {
		return // never started: nothing to stop, but Close still marks it closed
	}
	cancel()
	<-done
}
