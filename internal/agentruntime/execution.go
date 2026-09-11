package agentruntime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/ares_events"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// ExecutionConfig assembles the shared L2 execution core over caller-owned
// fabrics. The caller keeps ownership of recovery, chaos, evolution, IPC and
// transport; this config supplies only what the L2 session execution needs.
type ExecutionConfig struct {
	// Fabric is the task fabric the session nodes compile into (required).
	Fabric *taskfabric.Fabric
	// Agents is the agent fabric the planner reads cognitive state from and
	// the executing peers live in (required).
	Agents *agentfabric.Fabric
	// ChatClient sends chat messages with tool support (required).
	ChatClient agentfabric.ChatClient
	// ToolBinder advertises tool schemas to the planner (required).
	ToolBinder agentfabric.ToolBinder
	// StrategySource is the optional live evolution strategy (nil = none).
	StrategySource agents.StrategySource
	// L1DAG is the optional L1 ToolClass capability graph the planner reads
	// for enabled/budget/prior before growing L2 tool nodes (nil = permissive).
	L1DAG *engine.MutableDAG
	// MaxPlanDepth caps L2 growth depth (0 = planner default).
	MaxPlanDepth int
	// ReaperGrace is the terminal-task reaper's read-window grace (0 = 30s).
	ReaperGrace time.Duration
	// SessionIdleTTL is the idle window after which a session is released by
	// the sweeper (0 = agentfabric default). A non-positive value keeps the
	// registry default.
	SessionIdleTTL time.Duration
	// CompileStore is the event store the compile coordinator records
	// provenance to (optional; nil = no provenance events).
	CompileStore ares_events.EventStore
	// Logger is the logger shared by the cognition bodies (nil = default).
	Logger *slog.Logger
}

// Execution is the shared L2 execution core: the session registry, the
// incremental compile coordinator, the planner/router cognition, and the
// session task reaper. Both cmd/ares (serve) and sdk build one over their own
// fabrics, so "how an agent runs" has a single implementation.
type Execution struct {
	// Sessions is the per-session lifecycle helper (admission/release/etc.).
	Sessions *Sessions
	// Compile is the incremental projection coordinator (graph events → tasks).
	Compile *planprojection.CompileCoordinator
	// Router is the L2 cognition dispatched by capability for every peer.
	Router agentfabric.Cognition
	// Reaper harvests terminal tasks of RELEASED sessions (keep-set = live
	// sessions). The caller runs its loop.
	Reaper *taskfabric.Reaper
	// SessionIdleTTL is the effective idle TTL the caller's sweeper should use.
	SessionIdleTTL time.Duration
}

// NewExecution builds the shared L2 execution core. It fails loud on any
// missing required dependency so a partially wired runtime can never start.
func NewExecution(cfg ExecutionConfig) (*Execution, error) {
	if cfg.Fabric == nil {
		return nil, fmt.Errorf("agentruntime: execution requires a task fabric")
	}
	if cfg.Agents == nil {
		return nil, fmt.Errorf("agentruntime: execution requires an agent fabric")
	}
	if cfg.ChatClient == nil {
		return nil, fmt.Errorf("agentruntime: execution requires a chat client")
	}
	if cfg.ToolBinder == nil {
		return nil, fmt.Errorf("agentruntime: execution requires a tool binder")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	reg := agentfabric.NewSessionRegistry()
	compile := planprojection.NewCompileCoordinator(cfg.Fabric, cfg.CompileStore)

	planner, err := agentfabric.NewPlannerCognition(agentfabric.PlannerDeps{
		ChatClient:     cfg.ChatClient,
		ToolBinder:     cfg.ToolBinder,
		Sessions:       reg,
		Fabric:         cfg.Fabric,
		L1DAG:          cfg.L1DAG,
		StrategySource: cfg.StrategySource,
		AgentFabric:    cfg.Agents,
		MaxDepth:       cfg.MaxPlanDepth,
		Logger:         logger,
	})
	if err != nil {
		return nil, fmt.Errorf("agentruntime: create planner cognition: %w", err)
	}
	router := agentfabric.NewRouterCognitionWithPlanner(cfg.ToolBinder, planner, reg, logger)

	grace := cfg.ReaperGrace
	if grace <= 0 {
		grace = 30 * time.Second
	}
	reaper := taskfabric.NewReaperWithKeep(cfg.Fabric, "sess/", grace, KeepSet(reg))

	ttl := cfg.SessionIdleTTL
	if ttl <= 0 {
		ttl = agentfabric.DefaultSessionIdleTTL
	}

	return &Execution{
		Sessions:       &Sessions{Reg: reg, Fabric: cfg.Fabric, Compile: compile},
		Compile:        compile,
		Router:         router,
		Reaper:         reaper,
		SessionIdleTTL: ttl,
	}, nil
}

// AdmitSession is a convenience for Execution.Sessions.Admit.
func (e *Execution) AdmitSession(ctx context.Context, sessionID, prompt string) error {
	if e == nil || e.Sessions == nil {
		return fmt.Errorf("agentruntime: execution is nil")
	}
	return e.Sessions.Admit(ctx, sessionID, prompt)
}
