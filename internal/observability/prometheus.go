package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusMetrics holds all Prometheus metric definitions for GoAgent.
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
	ActiveAgents           prometheus.Gauge
	LLMTokensTotal         *prometheus.GaugeVec
	EvolutionScoreGauge    *prometheus.GaugeVec

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
				Name: "goagent_llm_calls_total",
				Help: "Total number of LLM calls",
			},
			[]string{"model", "status"},
		),
		ToolCallsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "goagent_tool_calls_total",
				Help: "Total number of tool calls",
			},
			[]string{"tool", "status"},
		),
		AgentErrorsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "goagent_agent_errors_total",
				Help: "Total number of agent errors",
			},
			[]string{"agent", "phase"},
		),
		LLMCallDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "goagent_llm_call_duration_seconds",
				Help:    "LLM call duration in seconds",
				Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
			},
			[]string{"model"},
		),
		AgentStepDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "goagent_agent_step_duration_seconds",
				Help:    "Agent step duration in seconds",
				Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
			},
			[]string{"phase"},
		),
		ActiveAgents: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "goagent_active_agents",
				Help: "Number of currently active agents",
			},
		),
		LLMTokensTotal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "goagent_llm_tokens_total",
				Help: "Total LLM tokens used",
			},
			[]string{"model", "direction"},
		),
		CostUSDTotal: prometheus.NewSummaryVec(
			prometheus.SummaryOpts{
				Name:       "goagent_cost_usd_total",
				Help:       "Total cost in USD",
				Objectives: map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001},
			},
			[]string{"model", "session"},
		),
		EvolutionDeployTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "goagent_evolution_deploy_total",
				Help: "Total number of strategy deployments",
			},
			[]string{"status"},
		),
		EvolutionGuardrailTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "goagent_evolution_guardrail_total",
				Help: "Total number of guardrail triggers",
			},
			[]string{"code"},
		),
		EvolutionShadowTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "goagent_evolution_shadow_total",
				Help: "Total number of shadow evaluation results",
			},
			[]string{"result"},
		),
		EvolutionScoreGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "goagent_evolution_score",
				Help: "Current evolution score by strategy ID",
			},
			[]string{"strategy_id"},
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
