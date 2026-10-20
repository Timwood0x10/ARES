package ares_bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/agents"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
)

// CandidateReplayConfig is what the per-candidate runtime replay needs from the process.
//
// ChatClient comes from this package's own LLM wiring. ToolBinder does NOT: the serving
// tool registry is assembled by cmd/ares (newToolBinder over the internal core.Registry),
// so it has to be handed in. That asymmetry is why this is a config rather than a
// constructor that reaches for globals — and why the caller passing nil is refused
// loudly instead of producing an environment whose candidates have no tools at all.
type CandidateReplayConfig struct {
	// ChatClient is the model client every candidate's agents talk to (required).
	ChatClient agentfabric.ChatClient
	// ToolBinder is the serving tool surface, sandboxed per candidate (required).
	ToolBinder agentfabric.ToolBinder
	// Await bounds one case's wait for a terminal answer (zero = package default).
	Await agentruntime.AwaitOptions
	// ToolCeiling overrides the evaluation tool ceiling (nil = agentruntime default).
	ToolCeiling []string
	// Logger receives the cost line (nil = package default).
	Logger *slog.Logger
}

// CandidateReplayFactory builds one isolated runtime per candidate.
//
// It exists so that "score this candidate by RUNNING it" is a constructor away rather
// than a re-assembly: the pieces that are hard to get right — fabric isolation, the
// strategy seam, the tool ceiling, the executor adapter — live in agentruntime, and this
// type only decides what the process supplies.
//
// It is OPT-IN. The gate that consumes it is armed by default (RegressionEnabled nil =
// auto), and a runtime-backed scorer changes the cost of that default by orders of
// magnitude: every candidate runs every case for real, against a real model and real
// tools, instead of two batched LLM calls. So the flag is explicit and the cost is
// logged at startup — a default that silently multiplies spend is not a default, it is
// a surprise.
type CandidateReplayFactory struct {
	cfg CandidateReplayConfig
}

// NewCandidateReplayFactory validates and returns the factory.
//
// Missing pieces are refused by name: an enabled replay that cannot run would score every
// candidate as "no signal", and no signal is not a low score — it is the absence of a
// measurement that the rest of the pipeline would happily treat as one.
func NewCandidateReplayFactory(cfg CandidateReplayConfig) (*CandidateReplayFactory, error) {
	switch {
	case cfg.ChatClient == nil:
		return nil, fmt.Errorf("bootstrap: candidate replay requires a chat client")
	case cfg.ToolBinder == nil:
		return nil, fmt.Errorf("bootstrap: candidate replay requires a tool binder (the serving tool registry lives in cmd/ares)")
	}
	return &CandidateReplayFactory{cfg: cfg}, nil
}

// Runtime assembles, starts and returns one candidate's runtime.
//
// The caller owns the returned runtime and MUST Close it: one scheduler goroutine and one
// pair of fabrics per candidate, held until then.
func (f *CandidateReplayFactory) Runtime(ctx context.Context, candidate *agents.ActiveStrategy) (*agentruntime.CandidateRuntime, error) {
	if candidate == nil {
		return nil, fmt.Errorf("bootstrap: candidate replay needs a candidate strategy")
	}
	rt, err := agentruntime.NewCandidateRuntime(agentruntime.CandidateRuntimeConfig{
		ChatClient:  f.cfg.ChatClient,
		ToolBinder:  f.cfg.ToolBinder,
		Await:       f.cfg.Await,
		ToolCeiling: f.cfg.ToolCeiling,
		Logger:      f.cfg.Logger,
	}, candidate)
	if err != nil {
		return nil, err
	}
	rt.Start(ctx)
	f.logArmed(candidate, rt)
	return rt, nil
}

// logArmed reports the boundary and the cost of the run that is about to happen.
//
// Both halves matter: the tool list is how an operator interprets a score ("it could not
// search the web" changes what a low result means), and the cost line is what makes the
// opt-in decision reviewable after the fact instead of only on the invoice.
func (f *CandidateReplayFactory) logArmed(candidate *agents.ActiveStrategy, rt *agentruntime.CandidateRuntime) {
	tools := rt.Tools()
	logger := f.cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("bootstrap: candidate runtime replay armed",
		"candidate", candidate.ID,
		"tools", tools,
		"tool_count", len(tools),
		"cost", "this candidate runs every case through the REAL runtime: a real model and real tool calls per case, not a batched judge prompt")
}
