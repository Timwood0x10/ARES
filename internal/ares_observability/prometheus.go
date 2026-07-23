package ares_observability

import (
	"net/http"

	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	// Histograms
	LLMCallDuration   *prometheus.HistogramVec
	AgentStepDuration *prometheus.HistogramVec

	// Gauges
	ActiveAgents        prometheus.Gauge
	LLMTokensTotal      *prometheus.GaugeVec
	EvolutionScoreGauge *prometheus.GaugeVec

	// Summary
	CostUSDTotal *prometheus.SummaryVec

	// AKG quality-gate observability (compiler Phase 1 L3). These gauges
	// reflect the latest compiler.AKGMetrics.Snapshot() pushed via
	// SetAKGSnapshot; they use Set (not Add) so repeated pushes never
	// double-count across runs.
	AKGObjectsIn         prometheus.Gauge
	AKGDroppedStructural prometheus.Gauge
	AKGDroppedLowConf    prometheus.Gauge
	AKGDedupHits         prometheus.Gauge
	AKGObjectsBuilt      prometheus.Gauge
	AKGConfidenceBucket  *prometheus.GaugeVec // label: confidence bucket
	AKGSignalTier        *prometheus.GaugeVec // label: signal tier
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
			[]string{"model", "session"},
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
		AKGObjectsIn: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_akg_objects_in",
				Help: "AKG quality gate: candidate nodes (entities+facts+references) considered by the selector",
			},
		),
		AKGDroppedStructural: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_akg_dropped_structural",
				Help: "AKG quality gate: nodes dropped for failing the structural gate (L1)",
			},
		),
		AKGDroppedLowConf: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_akg_dropped_lowconf",
				Help: "AKG quality gate: nodes dropped for low confidence (L2)",
			},
		),
		AKGDedupHits: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_akg_dedup_hits",
				Help: "AKG quality gate: near-duplicate objects discarded by the resolver (Jaccard)",
			},
		),
		AKGObjectsBuilt: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "ARES_akg_objects_built",
				Help: "AKG quality gate: objects that survived all gates and were projected into the AKG store",
			},
		),
		AKGConfidenceBucket: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ARES_akg_confidence_bucket",
				Help: "AKG quality gate: surviving objects per confidence bucket (<0.4, 0.4-0.7, 0.7-0.9, 0.9-1.0)",
			},
			[]string{"bucket"},
		),
		AKGSignalTier: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "ARES_akg_signal_tier",
				Help: "AKG quality gate: surviving objects per L2 signal tier (weak, medium, strong)",
			},
			[]string{"tier"},
		),
	}

	// Register all collectors with the default Prometheus registry.
	collectors := []prometheus.Collector{
		m.LLMCallsTotal,
		m.ToolCallsTotal,
		m.AgentErrorsTotal,
		m.LLMCallDuration,
		m.AgentStepDuration,
		m.ActiveAgents,
		m.LLMTokensTotal,
		m.CostUSDTotal,
		m.EvolutionDeployTotal,
		m.EvolutionGuardrailTotal,
		m.EvolutionShadowTotal,
		m.EvolutionScoreGauge,
		m.AKGObjectsIn,
		m.AKGDroppedStructural,
		m.AKGDroppedLowConf,
		m.AKGDedupHits,
		m.AKGObjectsBuilt,
		m.AKGConfidenceBucket,
		m.AKGSignalTier,
	}
	for _, c := range collectors {
		if err := prometheus.Register(c); err != nil {
			// AreAlreadyRegisteredError is acceptable in tests or multi-init scenarios.
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				return nil, err
			}
		}
	}

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
	m.CostUSDTotal.WithLabelValues(model, sessionID).Observe(costUSD)
}

// SetAKGSnapshot pushes a compiler AKG quality-gate snapshot onto the AKG
// Prometheus gauges. It uses Set (not Add) so repeated calls reflect the latest
// run without double-counting across independent pipeline runs.
//
// The compiler package stays free of any Prometheus dependency; this method is
// the single bridge point where the observability layer ingests a
// compiler.AKGMetrics.Snapshot(). Call it from the serve/metrics layer after a
// Compile (or periodically) to make the gate's effect visible on /metrics.
//
// Args:
//   - s: a *compiler.AKGSnapshot; nil is a no-op.
func (m *PrometheusMetrics) SetAKGSnapshot(s *compiler.AKGSnapshot) {
	if m == nil || s == nil {
		return
	}
	m.AKGObjectsIn.Set(float64(s.NodesIn))
	m.AKGDroppedStructural.Set(float64(s.DroppedStructural))
	m.AKGDroppedLowConf.Set(float64(s.DroppedLowConf))
	m.AKGDedupHits.Set(float64(s.DedupHits))
	m.AKGObjectsBuilt.Set(float64(s.ObjectsBuilt))
	for bucket, n := range s.ConfidenceHistogram {
		m.AKGConfidenceBucket.WithLabelValues(bucket).Set(float64(n))
	}
	for tier, n := range s.SignalTiers {
		m.AKGSignalTier.WithLabelValues(tier).Set(float64(n))
	}
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
