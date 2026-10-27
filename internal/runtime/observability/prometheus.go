package observability

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// cachedMetrics holds the first successfully registered instance so repeated
// initializations return the already-registered collectors instead of silently
// creating unregistered ones (whose recordings would never reach /metrics).
// Guarded by cachedMu (#race): concurrent NewPrometheusMetrics calls raced on
// the plain variable.
var (
	cachedMu      sync.Mutex
	cachedMetrics *PrometheusMetrics
)

// PrometheusMetrics holds all Prometheus metric definitions for ARES.
type PrometheusMetrics struct {
	// Counters
	LLMCallsTotal           *prometheus.CounterVec
	ToolCallsTotal          *prometheus.CounterVec
	AgentErrorsTotal        *prometheus.CounterVec
	EvolutionDeployTotal    *prometheus.CounterVec
	EvolutionGuardrailTotal *prometheus.CounterVec
	EvolutionShadowTotal    *prometheus.CounterVec

	// Lifecycle-specific counters for promote/rollback/gate-reject.
	EvolutionPromoteTotal    *prometheus.CounterVec
	EvolutionRollbackTotal   *prometheus.CounterVec
	EvolutionGateRejectTotal *prometheus.CounterVec

	// Experiment-log audit counters (M3-6). They answer "how many experiments, how were the
	// gates decided, and did the runtime patch land" without reading the JSONL on the host.
	EvolutionExperimentsTotal          *prometheus.CounterVec // {outcome}
	EvolutionExperimentSealErrorsTotal *prometheus.CounterVec // {reason}
	EvolutionGateDecisionsTotal        *prometheus.CounterVec // {gate,decision}
	EvolutionRuntimePatchesTotal       *prometheus.CounterVec // {result}

	// Gate-absence accounting + runtime attribution/residency gauges.
	EvolutionGateSkippedTotal *prometheus.CounterVec
	EvolutionWindowSamples    *prometheus.GaugeVec

	// Evolution loop introspection gauges. These expose the
	// current generation, DAG version, and compile count so /metrics
	// can answer "which generation, which gate, which compile".
	EvolutionGeneration   prometheus.Gauge
	EvolutionDAGVersion   prometheus.Gauge
	EvolutionCompileCount prometheus.Gauge

	// Histograms
	LLMCallDuration   *prometheus.HistogramVec
	AgentStepDuration *prometheus.HistogramVec

	// Scheduler histograms (M0 baseline). These answer the three
	// structural questions about the scheduling loop:
	//   - how long after a task becomes READY does it get dispatched?
	//   - when a slow quantum is in flight, how long before an idle agent
	//     picks up OTHER ready work (the work-conserving gap)?
	//   - how old is a task when it finally gets dispatched (fairness/aging)?
	SchedulerReadyToDispatchSeconds   *prometheus.HistogramVec
	SchedulerWorkConservingGapSeconds *prometheus.HistogramVec
	SchedulerReadyAgeSeconds          *prometheus.HistogramVec

	// Gauges
	ActiveAgents        prometheus.Gauge
	LLMTokensTotal      *prometheus.GaugeVec
	EvolutionScoreGauge *prometheus.GaugeVec

	// Shadow win-rate gauge — the fraction of shadow comparisons the
	// candidate won in the current window. Updated after each shadow
	// evaluation cycle.
	EvolutionShadowWinRate prometheus.Gauge

	// How long the currently active strategy has been promoted (seconds).
	EvolutionActiveDuration prometheus.Gauge

	// Summary
	CostUSDTotal *prometheus.SummaryVec
}

// NewPrometheusMetrics creates and registers all Prometheus metrics with the
// default registry. Returns an error if registration fails.
//
// Returns:
//   - *PrometheusMetrics: initialized metrics instance.
//   - error: non-nil if any metric registration fails.
func NewPrometheusMetrics() (*PrometheusMetrics, error) {
	m := &PrometheusMetrics{
		LLMCallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_llm_calls_total",
				Help: "Total number of LLM calls",
			},
			[]string{"model", "status"},
		),
		ToolCallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_tool_calls_total",
				Help: "Total number of tool calls",
			},
			[]string{"tool", "status"},
		),
		AgentErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_agent_errors_total",
				Help: "Total number of agent errors",
			},
			[]string{"agent", "phase"},
		),
		LLMCallDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "ARES_llm_call_duration_seconds",
				Help:    "LLM call duration in seconds",
				Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"model"},
		),
		AgentStepDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "ARES_agent_step_duration_seconds",
				Help:    "Agent step duration in seconds",
				Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
			},
			[]string{"phase"},
		),
		SchedulerReadyToDispatchSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "ARES_scheduler_ready_to_dispatch_seconds",
				Help:    "Wait of work that was actually dispatched: READY to lease acquired and agent chosen. A task that leaves the queue without a claim (no capable candidate, exhausted budget) contributes nothing, so this histogram describes runs, not attempts",
				Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{},
		),
		SchedulerWorkConservingGapSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "ARES_scheduler_work_conserving_gap_seconds",
				Help:    "Age of the scheduler's current continuously-busy interval when it had to leave READY work behind: the interval starts at the first quantum admitted after an empty pipeline and ends when the last one finishes. It is NOT saturation — capacity 4 running one long quantum still accumulates, so a deferral after that quantum reports the whole interval. Overlapping quanta count as one interval rather than restarting per completion. Records only deferrals: a zero would claim no work waited",
				Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{},
		),
		SchedulerReadyAgeSeconds: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "ARES_scheduler_ready_age_seconds",
				Help:    "Queue age when the scheduler looked: once when a task leaves the queue, and at most once per five seconds for the head of a deferred queue. TIME-WEIGHTED — a stalled queue contributes repeatedly while a fast one contributes once per task, so this estimates how old the backlog is, not the wait of an average task. Includes work that was never dispatched",
				Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
			},
			[]string{},
		),
		ActiveAgents: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_active_agents",
				Help: "Number of currently active agents",
			},
		),
		LLMTokensTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ARES_llm_tokens_total",
				Help: "Total LLM tokens used",
			},
			[]string{"model", "direction"},
		),
		CostUSDTotal: prometheus.NewSummaryVec(
			prometheus.SummaryOpts{
				Name:       "ARES_cost_usd_total",
				Help:       "Total cost in USD",
				Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
			},
			// model only (#cardinality): session IDs are unbounded and would
			// grow the registry forever; per-session detail lives in
			// CostTracker (cost.go).
			[]string{"model"},
		),
		EvolutionDeployTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_deploy_total",
				Help: "Total number of strategy deployments",
			},
			[]string{"status"},
		),
		EvolutionGuardrailTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_guardrail_total",
				Help: "Total number of guardrail triggers",
			},
			[]string{"code"},
		),
		EvolutionShadowTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_shadow_total",
				Help: "Total number of shadow evaluation results",
			},
			[]string{"result"},
		),
		EvolutionScoreGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ARES_evolution_score",
				Help: "Current evolution score by strategy ID",
			},
			[]string{"strategy_id"},
		),
		EvolutionPromoteTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_promote_total",
				Help: "Total number of strategy promotions by result",
			},
			[]string{"result"},
		),
		EvolutionRollbackTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_rollback_total",
				Help: "Total number of strategy rollbacks by reason",
			},
			[]string{"reason"},
		),
		EvolutionGateRejectTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_gate_reject_total",
				Help: "Total number of verify-gate rejections by gate name",
			},
			[]string{"gate"},
		),
		EvolutionExperimentsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_experiments_total",
				Help: "Total experiment records sealed, by outcome",
			},
			[]string{"outcome"},
		),
		EvolutionExperimentSealErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_experiment_seal_errors_total",
				Help: "Total failures to seal an experiment record, by reason",
			},
			[]string{"reason"},
		),
		EvolutionGateDecisionsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_gate_decisions_total",
				Help: "Total gate verdicts recorded, by gate and decision",
			},
			[]string{"gate", "decision"},
		),
		EvolutionRuntimePatchesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_runtime_patches_total",
				Help: "Total runtime patches the coordinator applied, by result",
			},
			[]string{"result"},
		),
		EvolutionShadowWinRate: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_evolution_shadow_win_rate",
				Help: "Fraction of shadow comparisons won by the candidate strategy",
			},
		),
		EvolutionGateSkippedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "ARES_evolution_gate_skipped_total",
				Help: "Total number of verify gates deliberately NOT registered, by gate and reason",
			},
			[]string{"gate", "reason"},
		),
		EvolutionActiveDuration: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_evolution_active_duration_seconds",
				Help: "How long the currently active strategy has been promoted, in seconds",
			},
		),
		EvolutionWindowSamples: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ARES_evolution_window_samples",
				Help: "Runtime fitness evidence window sample count, by strategy and source",
			},
			[]string{"strategy_id", "source"},
		),
		EvolutionGeneration: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_evolution_generation",
				Help: "Current evolution generation number",
			},
		),
		EvolutionDAGVersion: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_evolution_dag_version",
				Help: "Current live MutableDAG version (mutation counter)",
			},
		),
		EvolutionCompileCount: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_evolution_compile_count",
				Help: "Total number of CompilePlan calls since startup",
			},
		),
	}

	// Register all collectors with the default Prometheus registry.
	collectors := []prometheus.Collector{
		m.LLMCallsTotal,
		m.ToolCallsTotal,
		m.AgentErrorsTotal,
		m.LLMCallDuration,
		m.AgentStepDuration,
		m.SchedulerReadyToDispatchSeconds,
		m.SchedulerWorkConservingGapSeconds,
		m.SchedulerReadyAgeSeconds,
		m.ActiveAgents,
		m.LLMTokensTotal,
		m.CostUSDTotal,
		m.EvolutionDeployTotal,
		m.EvolutionGuardrailTotal,
		m.EvolutionShadowTotal,
		m.EvolutionScoreGauge,
		m.EvolutionPromoteTotal,
		m.EvolutionRollbackTotal,
		m.EvolutionGateRejectTotal,
		m.EvolutionExperimentsTotal,
		m.EvolutionExperimentSealErrorsTotal,
		m.EvolutionGateDecisionsTotal,
		m.EvolutionRuntimePatchesTotal,
		m.EvolutionShadowWinRate,
		m.EvolutionGateSkippedTotal,
		m.EvolutionActiveDuration,
		m.EvolutionWindowSamples,
		m.EvolutionGeneration,
		m.EvolutionDAGVersion,
		m.EvolutionCompileCount,
	}
	for _, c := range collectors {
		if err := prometheus.Register(c); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
				// Collectors are already registered (previous init or tests).
				// Return the cached instance so the caller records into the
				// registered vectors; without a cache there is no safe way to
				// recover the existing instance, so fail loudly rather than
				// returning an unregistered metric set.
				cachedMu.Lock()
				cm := cachedMetrics
				cachedMu.Unlock()
				if cm != nil {
					return cm, nil
				}
				return nil, fmt.Errorf("prometheus: collectors already registered and no cached instance available: %w", err)
			}
			return nil, err
		}
	}

	cachedMu.Lock()
	cachedMetrics = m
	cachedMu.Unlock()

	return m, nil
}

// RecordLLMCall increments the LLM call counter and records its duration.
//
// Args:
//   - model: the LLM model name.
//   - status: call result status (e.g., "success", "error").
//   - durationSeconds: how long the call took in seconds.
func (m *PrometheusMetrics) RecordLLMCall(model, status string, durationSeconds float64) {
	if m == nil {
		return
	}
	m.LLMCallsTotal.WithLabelValues(model, status).Inc()
	m.LLMCallDuration.WithLabelValues(model).Observe(durationSeconds)
}

// RecordToolCall increments the tool call counter with tool name and status.
//
// Args:
//   - toolName: the name of the tool called.
//   - status: call result status (e.g., "success", "error").
func (m *PrometheusMetrics) RecordToolCall(toolName, status string) {
	if m == nil {
		return
	}
	m.ToolCallsTotal.WithLabelValues(toolName, status).Inc()
}

// RecordAgentError increments the agent error counter.
//
// Args:
//   - agentID: the agent identifier.
//   - phase: the phase where the error occurred (e.g., "planning", "execution").
func (m *PrometheusMetrics) RecordAgentError(agentID, phase string) {
	if m == nil {
		return
	}
	m.AgentErrorsTotal.WithLabelValues(agentID, phase).Inc()
}

// RecordAgentStepDuration records an agent step duration observation.
//
// Args:
//   - phase: the step phase name (e.g., "planning", "execution").
//   - durationSeconds: how long the step took in seconds.
func (m *PrometheusMetrics) RecordAgentStepDuration(phase string, durationSeconds float64) {
	if m == nil {
		return
	}
	m.AgentStepDuration.WithLabelValues(phase).Observe(durationSeconds)
}

// SetActiveAgents sets the active agents gauge to the given value.
//
// Args:
//   - count: current number of active agents.
func (m *PrometheusMetrics) SetActiveAgents(count float64) {
	if m == nil {
		return
	}
	m.ActiveAgents.Set(count)
}

// IncActiveAgents increments the active agents gauge by 1.
func (m *PrometheusMetrics) IncActiveAgents() {
	if m == nil {
		return
	}
	m.ActiveAgents.Inc()
}

// DecActiveAgents decrements the active agents gauge by 1.
func (m *PrometheusMetrics) DecActiveAgents() {
	if m == nil {
		return
	}
	m.ActiveAgents.Dec()
}

// RecordLLMTokens sets the token gauge for a model and direction.
//
// Args:
//   - model: the LLM model name.
//   - direction: token direction ("input" or "output").
//   - count: total token count.
func (m *PrometheusMetrics) RecordLLMTokens(model, direction string, count float64) {
	if m == nil {
		return
	}
	m.LLMTokensTotal.WithLabelValues(model, direction).Set(count)
}

// RecordEvolutionDeploy increments the evolution deploy counter.
//
// Args:
//   - status: deployment status ("success", "rollback").
func (m *PrometheusMetrics) RecordEvolutionDeploy(status string) {
	if m == nil {
		return
	}
	m.EvolutionDeployTotal.WithLabelValues(status).Inc()
}

// RecordEvolutionGuardrail increments the evolution guardrail trigger counter.
//
// Args:
//   - code: the guardrail error code.
func (m *PrometheusMetrics) RecordEvolutionGuardrail(code string) {
	if m == nil {
		return
	}
	m.EvolutionGuardrailTotal.WithLabelValues(code).Inc()
}

// RecordEvolutionShadow increments the shadow evaluation result counter.
//
// Args:
//   - result: evaluation result ("promoted", "rejected").
func (m *PrometheusMetrics) RecordEvolutionShadow(result string) {
	if m == nil {
		return
	}
	m.EvolutionShadowTotal.WithLabelValues(result).Inc()
}

// SetEvolutionScore sets the current score for a strategy ID.
//
// Args:
//   - strategyID: the strategy identifier.
//   - score: the current score value.
func (m *PrometheusMetrics) SetEvolutionScore(strategyID string, score float64) {
	if m == nil {
		return
	}
	m.EvolutionScoreGauge.WithLabelValues(strategyID).Set(score)
}

// RecordEvolutionPromote increments the promote counter for the given result
// ("success", "deploy_failed").
//
// Args:
//   - result: the promotion outcome.
func (m *PrometheusMetrics) RecordEvolutionPromote(result string) {
	if m == nil {
		return
	}
	m.EvolutionPromoteTotal.WithLabelValues(result).Inc()
}

// RecordEvolutionRollback increments the rollback counter for the given reason
// ("degradation", "guardrail", "manual").
//
// Args:
//   - reason: the rollback trigger reason.
func (m *PrometheusMetrics) RecordEvolutionRollback(reason string) {
	if m == nil {
		return
	}
	m.EvolutionRollbackTotal.WithLabelValues(reason).Inc()
}

// RecordEvolutionGateReject increments the gate-reject counter for the given
// gate name.
//
// Args:
//   - gate: the name of the verify gate that rejected the candidate.
func (m *PrometheusMetrics) RecordEvolutionGateReject(gate string) {
	if m == nil {
		return
	}
	m.EvolutionGateRejectTotal.WithLabelValues(gate).Inc()
}

// SetEvolutionShadowWinRate sets the current shadow win-rate gauge value
// in [0,1].
//
// Args:
//   - rate: the fraction of shadow comparisons won by the candidate.
func (m *PrometheusMetrics) SetEvolutionShadowWinRate(rate float64) {
	if m == nil {
		return
	}
	m.EvolutionShadowWinRate.Set(rate)
}

// RecordEvolutionGateSkipped increments the gate-skipped counter: a gate
// that was deliberately NOT registered at wiring time. Without it, an absent
// gate exists only in the startup log line.
//
// Args:
//   - gate: the gate name (e.g. "shadow").
//   - reason: the documented skip reason (fixed vocabulary, not free text).
func (m *PrometheusMetrics) RecordEvolutionGateSkipped(gate, reason string) {
	if m == nil {
		return
	}
	m.EvolutionGateSkippedTotal.WithLabelValues(gate, reason).Inc()
}

// SetEvolutionActiveDuration sets the current active-strategy residency gauge
// in seconds. Paired with the promote throttle it exposes strategy
// churn: a gauge pinned near zero means strategies rotate faster than the
// rollback window accumulates evidence.
//
// Args:
//   - seconds: seconds since the current strategy was promoted.
func (m *PrometheusMetrics) SetEvolutionActiveDuration(seconds float64) {
	if m == nil {
		return
	}
	m.EvolutionActiveDuration.Set(seconds)
}

// SetEvolutionWindowSamples sets the fitness-window sample-count gauge for
// one strategy/source pair. This is the runtime check that the
// attribution actually distributes samples across strategies: if stamping
// breaks, every sample piles up under a single strategy_id label value.
//
// Args:
//   - strategyID: the attributed strategy.
//   - source: the evidence source (e.g. "strategy").
//   - count: the current window sample count.
func (m *PrometheusMetrics) SetEvolutionWindowSamples(strategyID, source string, count int) {
	if m == nil {
		return
	}
	m.EvolutionWindowSamples.WithLabelValues(strategyID, source).Set(float64(count))
}

// SetEvolutionGeneration sets the current evolution generation gauge.
// This exposes the GA's generation counter so /metrics can answer
// "which generation produced this compile".
//
// Args:
//   - generation: the current evolution generation.
func (m *PrometheusMetrics) SetEvolutionGeneration(generation int) {
	if m == nil {
		return
	}
	m.EvolutionGeneration.Set(float64(generation))
}

// SetEvolutionDAGVersion sets the current live DAG version gauge.
// This is MutableDAG.Version() — the mutation counter that increments on
// every structural patch (AddNode/RemoveNode/AddEdge/ReplaceNode).
//
// Args:
//   - version: the current DAG version.
func (m *PrometheusMetrics) SetEvolutionDAGVersion(version uint64) {
	if m == nil {
		return
	}
	m.EvolutionDAGVersion.Set(float64(version))
}

// SetEvolutionCompileCount sets the total compile count gauge.
// This is the number of CompilePlan calls since startup, exposing whether
// the projection pipeline is actually firing (a flat zero means recompile
// events are not reaching the coordinator).
//
// Args:
//   - count: the total number of compiles.
func (m *PrometheusMetrics) SetEvolutionCompileCount(count uint64) {
	if m == nil {
		return
	}
	m.EvolutionCompileCount.Set(float64(count))
}

// RecordSchedulerReadyToDispatch observes the time from a task becoming READY
// to the scheduler acquiring it. nil-safe: a no-op when metrics are not wired.
func (m *PrometheusMetrics) RecordSchedulerReadyToDispatch(seconds float64) {
	if m == nil {
		return
	}
	m.SchedulerReadyToDispatchSeconds.WithLabelValues().Observe(seconds)
}

// RecordSchedulerWorkConservingGap observes the delay in dispatching new READY
// work while a slow quantum is in flight. nil-safe.
func (m *PrometheusMetrics) RecordSchedulerWorkConservingGap(seconds float64) {
	if m == nil {
		return
	}
	m.SchedulerWorkConservingGapSeconds.WithLabelValues().Observe(seconds)
}

// RecordSchedulerReadyAge observes a task's age (since it became READY) at the
// moment it is dispatched. nil-safe.
func (m *PrometheusMetrics) RecordSchedulerReadyAge(seconds float64) {
	if m == nil {
		return
	}
	m.SchedulerReadyAgeSeconds.WithLabelValues().Observe(seconds)
}

// RecordCost observes a cost value for a model and session.
//
// Args:
//   - model: the LLM model name.
//   - sessionID: the session identifier.
//   - costUSD: the cost in USD.
func (m *PrometheusMetrics) RecordCost(model, sessionID string, costUSD float64) {
	if m == nil {
		return
	}
	// sessionID intentionally not a label (#cardinality): sessions are
	// unbounded; per-session tracking stays in CostTracker.
	m.CostUSDTotal.WithLabelValues(model).Observe(costUSD)
}

// MetricsHTTPHandler returns an http.Handler that serves Prometheus metrics
// at the /metrics endpoint.
func MetricsHTTPHandler() http.Handler {
	return promhttp.Handler()
}

// RegisterMetricsRouter registers the /metrics endpoint on the given ServeMux.
// This is a convenience function for integrating Prometheus metrics into
// existing HTTP servers.
//
// Args:
//   - mux: the ServeMux to register the endpoint on.
func RegisterMetricsRouter(mux *http.ServeMux) {
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		MetricsHTTPHandler().ServeHTTP(w, r)
	})
}

// RecordEvolutionExperiment counts one sealed experiment record by outcome.
//
// It is the denominator for the seal-error counter: "3 failures" means nothing without "of how
// many", and the two must be readable side by side on the same dashboard.
func (m *PrometheusMetrics) RecordEvolutionExperiment(outcome string) {
	if m == nil {
		return
	}
	m.EvolutionExperimentsTotal.WithLabelValues(outcome).Inc()
}

// RecordEvolutionExperimentSealError counts a failure to persist an experiment record.
func (m *PrometheusMetrics) RecordEvolutionExperimentSealError(reason string) {
	if m == nil {
		return
	}
	m.EvolutionExperimentSealErrorsTotal.WithLabelValues(reason).Inc()
}

// RecordEvolutionGateDecision counts one gate verdict. Both passes and failures are counted:
// EvolutionGateRejectTotal already tracks refusals, but a gate that never rejects and a gate that
// never runs look identical without the denominator.
func (m *PrometheusMetrics) RecordEvolutionGateDecision(gate, decision string) {
	if m == nil {
		return
	}
	m.EvolutionGateDecisionsTotal.WithLabelValues(gate, decision).Inc()
}

// RecordEvolutionRuntimePatch counts one runtime-patch apply attempt by result.
func (m *PrometheusMetrics) RecordEvolutionRuntimePatch(result string) {
	if m == nil {
		return
	}
	m.EvolutionRuntimePatchesTotal.WithLabelValues(result).Inc()
}
