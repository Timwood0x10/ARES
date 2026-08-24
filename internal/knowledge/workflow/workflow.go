package workflow

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// AgentTypeAKF is the agent type identifier for AKF workflow steps.
const AgentTypeAKF models.AgentType = "akf"

// PipelineStep identifies which AKF pipeline step to execute.
type PipelineStep string

const (
	StepBuildGraph PipelineStep = "build_graph"
	StepCompile    PipelineStep = "compile"
)

// StepConfig is the JSON configuration for an AKF workflow step.
type StepConfig struct {
	Step      PipelineStep `json:"step"`
	Goal      string       `json:"goal"`
	Formats   []string     `json:"formats,omitempty"`
	MaxTokens int          `json:"max_tokens,omitempty"`
	ForGraph  int          `json:"for_graph,omitempty"`
}

// KnowledgeAgent wraps AKF KnowledgeRuntime as a DAG workflow agent.
type KnowledgeAgent struct {
	id     string
	rt     *runtime.KnowledgeRuntime
	comp   compiler.Compiler
	cfg    StepConfig
	status atomic.Value // models.AgentStatus
}

// NewKnowledgeAgent creates an AKF agent for DAG workflow integration.
func NewKnowledgeAgent(id string, rt *runtime.KnowledgeRuntime, comp compiler.Compiler, cfg StepConfig) *KnowledgeAgent {
	a := &KnowledgeAgent{id: id, rt: rt, comp: comp, cfg: cfg}
	a.status.Store(models.AgentStatusReady)
	return a
}

func (a *KnowledgeAgent) ID() string                 { return a.id }
func (a *KnowledgeAgent) Type() models.AgentType     { return AgentTypeAKF }
func (a *KnowledgeAgent) Status() models.AgentStatus { return a.status.Load().(models.AgentStatus) }
func (a *KnowledgeAgent) Start(_ context.Context) error {
	a.status.Store(models.AgentStatusBusy)
	return nil
}
func (a *KnowledgeAgent) Stop(_ context.Context) error {
	a.status.Store(models.AgentStatusReady)
	return nil
}

// Process executes the configured AKF pipeline step.
// Input is expected to be a JSON string with StepConfig fields (overrides the
// agent's default config for this specific execution).
func (a *KnowledgeAgent) Process(ctx context.Context, input any) (any, error) {
	cfg := a.cfg

	// Parse input if provided (allows overrides per execution).
	if input != nil {
		switch v := input.(type) {
		case string:
			if v != "" {
				_ = json.Unmarshal([]byte(v), &cfg)
			}
		case []byte:
			if len(v) > 0 {
				_ = json.Unmarshal(v, &cfg)
			}
		}
	}

	if cfg.Goal == "" {
		return nil, fmt.Errorf("akf: goal is required")
	}

	budget := knowledge.TokenBudget{
		MaxTokens: cfg.MaxTokens,
		ForGraph:  cfg.ForGraph,
	}
	if cfg.MaxTokens <= 0 {
		// Only fill defaults for fields the caller did not explicitly set.
		// The previous code overwrote the whole budget, dropping a caller
		// supplied ForGraph whenever MaxTokens was left at its zero value.
		if budget.MaxTokens <= 0 {
			budget.MaxTokens = 5000
		}
		if budget.ForGraph <= 0 {
			budget.ForGraph = 3000
		}
	}
	if r := budget.MaxTokens - budget.ForGraph; r > 0 {
		budget.Reserved = r
	} else {
		// A negative Reserved would violate the budget invariant; clamp to
		// zero so ForGraph never exceeds the total budget.
		budget.Reserved = 0
	}

	switch cfg.Step {
	case StepCompile:
		graph, err := a.rt.Execute(ctx, cfg.Goal, budget, nil)
		if err != nil {
			return nil, fmt.Errorf("akf build: %w", err)
		}

		formats := []compiler.Format{compiler.FormatPrompt}
		if len(cfg.Formats) > 0 {
			formats = make([]compiler.Format, len(cfg.Formats))
			for i, f := range cfg.Formats {
				formats[i] = compiler.Format(f)
			}
		}

		compiled, err := a.comp.Compile(ctx, graph, compiler.CompileConfig{Formats: formats})
		if err != nil {
			return nil, fmt.Errorf("akf compile: %w", err)
		}

		return compiled, nil

	default: // StepBuildGraph
		graph, err := a.rt.Execute(ctx, cfg.Goal, budget, nil)
		if err != nil {
			return nil, fmt.Errorf("akf build: %w", err)
		}
		return graph, nil
	}
}

// ProcessStream is not supported for AKF agents (returns a single result).
func (a *KnowledgeAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := a.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	if err != nil {
		ch <- base.AgentEvent{Type: base.EventError, Err: err}
	} else {
		ch <- base.AgentEvent{Type: base.EventComplete, Data: result}
	}
	close(ch)
	return ch, nil
}

// AsGraphNode adapts the AKF agent to a sdk.Graph node: a
// func(ctx, state) error that reads its input from state["input"] and writes
// the step result to state["output"]. This lets AKF steps run inside the
// current DAG surface (sdk.Graph pure-function nodes) without routing through
// the kernel scheduling path — AKF is pure compute, nothing to schedule.
//
// Input contract: state["input"] is an OPTIONAL per-run override (JSON StepConfig).
// When absent, the step runs with the agent's configured StepConfig.Goal — a
// configuration-driven AKF step is legitimate and needs no upstream input. If
// BOTH are absent (no input override AND an empty configured Goal), Process
// returns "akf: goal is required" rather than silently producing an empty
// result — a missing upstream output surfaces as a node error, never as quiet
// degradation.
//
// Concurrency: each graph node should own its own KnowledgeAgent (via
// NewKnowledgeAgent). If one instance is shared by multiple concurrently
// executing nodes, Process must be safe for concurrent use itself — the
// returned adapter adds no synchronization.
//
// Beta: the graph-node adapter API is BETA and may change between releases.
func (a *KnowledgeAgent) AsGraphNode() func(ctx context.Context, state map[string]any) error {
	return func(ctx context.Context, state map[string]any) error {
		input := state["input"]
		out, err := a.Process(ctx, input)
		if err != nil {
			return err
		}
		state["output"] = out
		return nil
	}
}
