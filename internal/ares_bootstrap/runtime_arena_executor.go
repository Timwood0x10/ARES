package ares_bootstrap

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	evoService "github.com/Timwood0x10/ares/internal/runtime/ares_evolution/service"
)

// RuntimeArenaExecutor runs each arena case through a real per-candidate runtime.
//
// It is the execution half of P0-1's replay. The arena's JUDGE is untouched — the same
// grading prompt, the same parse, the same deterministic floor — so a score difference
// between two candidates is a difference in execution, not in judging.
//
// ONE RUNTIME PER CANDIDATE, NOT PER CASE. A runtime owns a pair of fabrics, a scheduler
// loop and a tool sandbox; building one per case would multiply that by the suite size for
// no gain, and would give every case a different execution environment — the comparison is
// supposed to differ in the candidate, not in the setup. Runtimes are therefore built on
// first use, keyed by candidate identity, and closed together by Close.
//
// The runtimes deliberately outlive any single case: a case's context is the check's
// timeout, and a drain loop started under it would die with the first slow case. That is
// why construction takes a base context and Execute takes the per-case one.
type RuntimeArenaExecutor struct {
	// provider resolves the factory on FIRST USE, not at construction. That is not
	// laziness for its own sake: the serving tool binder the factory needs is wired AFTER
	// the evolution components are built (cmd/ares: components at serve.go:108, toolchain
	// at :141), so anything resolved at boot would find a binder that does not exist yet.
	// Deferring matches what this type already does with runtimes.
	provider func() (*CandidateReplayFactory, error)
	factory  *CandidateReplayFactory // resolved once, on first use (guarded by mu)
	logger   *slog.Logger
	ctx      context.Context
	cancel   context.CancelFunc

	mu       sync.Mutex
	runtimes map[string]*agentruntime.CandidateRuntime
	// order is the runtime-use order, oldest first: the cache is BOUNDED to
	// retainRuntimes entries so a long-lived process cannot accumulate one pair of
	// fabrics per candidate it has ever compared.
	order  []string
	closed bool
}

// retainRuntimes is how many candidate runtimes stay alive at once.
//
// Two, because the comparison is an A/B: the baseline and the candidate alternate for the
// duration of the check. Anything older belongs to a comparison that has already finished —
// keeping it would hold a scheduler loop and two fabrics open for the life of the process,
// and a rolling deployment compares candidates indefinitely, so "one per candidate ever" is
// unbounded growth, not a cache.
const retainRuntimes = 2

// Compile-time proof that this is the execution seam the arena scorer expects; if either
// side's shape moves, this stops compiling instead of failing at wiring time.
var _ evoService.ArenaExecutor = (*RuntimeArenaExecutor)(nil)

// NewRuntimeArenaExecutor binds a candidate-runtime factory to the arena's execution step.
//
// base must outlive the whole comparison (the runtimes are started from it, not from the
// per-case contexts); factory must be non-nil, because an executor that cannot execute
// would make "replay is on" a lie the scores would never reveal.
func NewRuntimeArenaExecutor(base context.Context, provider func() (*CandidateReplayFactory, error), logger *slog.Logger) (*RuntimeArenaExecutor, error) {
	switch {
	case provider == nil:
		return nil, fmt.Errorf("bootstrap: runtime arena executor requires a factory provider (the serving tool binder is wired after bootstrap, so the factory cannot be built here)")
	case base == nil:
		return nil, fmt.Errorf("bootstrap: runtime arena executor requires a base context (the runtimes outlive any single case)")
	}
	ctx, cancel := context.WithCancel(base)
	if logger == nil {
		logger = slog.Default()
	}
	return &RuntimeArenaExecutor{
		provider: provider,
		logger:   logger,
		ctx:      ctx,
		cancel:   cancel,
		runtimes: make(map[string]*agentruntime.CandidateRuntime),
	}, nil
}

// resolvedFactory returns the factory, calling the provider at most once.
//
// Caller must NOT hold e.mu... it does hold it here: the call is guarded by the same mutex
// that guards the cache, so two concurrent cases cannot both build a factory. The provider
// itself only reads process state (a tool binder and a client), so it cannot re-enter this
// type.
func (e *RuntimeArenaExecutor) resolvedFactory() (*CandidateReplayFactory, error) {
	if e.factory != nil {
		return e.factory, nil
	}
	factory, err := e.provider()
	if err != nil {
		return nil, fmt.Errorf("bootstrap: resolve candidate replay factory: %w", err)
	}
	if factory == nil {
		return nil, fmt.Errorf("bootstrap: candidate replay factory provider returned nil — replay cannot run")
	}
	e.factory = factory
	return factory, nil
}

// Execute implements the arena scorer's execution seam: the candidate's own runtime
// produces the answer, and the caller grades it.
func (e *RuntimeArenaExecutor) Execute(ctx context.Context, strategy any, instructions, caseText string) (string, error) {
	rt, err := e.runtimeFor(strategy, instructions)
	if err != nil {
		return "", err
	}
	answer, _, _, err := rt.Executor().Execute(ctx, caseText)
	if err != nil {
		return "", fmt.Errorf("runtime replay: %w", err)
	}
	return answer, nil
}

// Close stops every runtime it built, in one place: a comparison that leaves its
// per-candidate loops running would keep their fabrics alive for the life of the process.
func (e *RuntimeArenaExecutor) Close() {
	e.mu.Lock()
	live := e.runtimes
	e.runtimes = nil
	e.closed = true
	e.mu.Unlock()

	for _, rt := range live {
		rt.Close()
	}
	e.cancel()
	e.logger.Info("bootstrap: runtime arena executor closed",
		"candidates_replayed", len(live),
		"note", "each candidate ran in its own fabrics; closing releases their drain loops")
}

// runtimeFor returns the candidate's runtime, building it on first use.
func (e *RuntimeArenaExecutor) runtimeFor(strategy any, instructions string) (*agentruntime.CandidateRuntime, error) {
	candidate, err := candidateFromArenaStrategy(strategy, instructions)
	if err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, fmt.Errorf("bootstrap: runtime arena executor is closed")
	}
	if rt, ok := e.runtimes[candidate.ID]; ok {
		e.touchLocked(candidate.ID)
		return rt, nil
	}
	factory, err := e.resolvedFactory()
	if err != nil {
		return nil, err
	}
	rt, err := factory.Runtime(e.ctx, candidate)
	if err != nil {
		return nil, err
	}
	e.runtimes[candidate.ID] = rt
	e.order = append(e.order, candidate.ID)
	e.evictLocked()
	return rt, nil
}

// touchLocked moves an id to the most-recently-used end.
func (e *RuntimeArenaExecutor) touchLocked(id string) {
	for i, seen := range e.order {
		if seen == id {
			e.order = append(append(e.order[:i:i], e.order[i+1:]...), id)
			return
		}
	}
}

// evictLocked closes runtimes beyond retainRuntimes, oldest first.
//
// Closing here rather than at process shutdown is the point: the candidate that lost its
// turn is finished, and its fabrics plus drain loop should be released while we know that.
// Caller must hold e.mu; Close on a runtime does not take e.mu, so this cannot deadlock.
func (e *RuntimeArenaExecutor) evictLocked() {
	for len(e.order) > retainRuntimes {
		oldest := e.order[0]
		e.order = e.order[1:]
		if rt, ok := e.runtimes[oldest]; ok {
			delete(e.runtimes, oldest)
			rt.Close()
			e.logger.Info("bootstrap: retired a candidate runtime no longer being compared",
				"candidate", oldest, "retained", retainRuntimes)
		}
	}
}

// candidateFromArenaStrategy converts what the arena handed over into the candidate the
// runtime runs.
//
// Two shapes are legitimate. A *mutation.Strategy is the whole candidate: its Params ride
// along, which is how a candidate's own tool whitelist reaches the runtime's sandbox
// (agents.ParamKeyTools). A bare string is what the CLI and dream-cycle paths pass — it has
// no Params, so that candidate runs on the evaluation ceiling, and saying so is better than
// inventing a tool set it never declared.
func candidateFromArenaStrategy(strategy any, instructions string) (*agents.ActiveStrategy, error) {
	switch v := strategy.(type) {
	case *mutation.Strategy:
		if v == nil {
			return nil, fmt.Errorf("bootstrap: runtime replay received a nil strategy")
		}
		return &agents.ActiveStrategy{
			ID:     candidateKey(v.ID, v.PromptTemplate),
			Prompt: v.PromptTemplate,
			Params: v.Params,
		}, nil
	case mutation.Strategy:
		return &agents.ActiveStrategy{
			ID:     candidateKey(v.ID, v.PromptTemplate),
			Prompt: v.PromptTemplate,
			Params: v.Params,
		}, nil
	case string:
		if v == "" {
			return nil, fmt.Errorf("bootstrap: runtime replay received empty instructions")
		}
		return &agents.ActiveStrategy{ID: candidateKey("", v), Prompt: v}, nil
	default:
		if instructions == "" {
			return nil, fmt.Errorf("bootstrap: runtime replay cannot read a strategy of type %T", strategy)
		}
		return &agents.ActiveStrategy{ID: candidateKey("", instructions), Prompt: instructions}, nil
	}
}

// candidateKey gives a candidate a stable, bounded identity for caching and logs.
//
// The id is used as a map key and printed on every runtime the factory arms, so a bare
// prompt template would either collide (two candidates, one cache entry) or print a
// multi-line prompt into the log. Hashing keeps it short and, unlike truncation, cannot
// make two different candidates look like one.
func candidateKey(id, instructions string) string {
	if id != "" {
		return id
	}
	sum := sha256.Sum256([]byte(instructions))
	return fmt.Sprintf("inline-%x", sum[:8])
}
