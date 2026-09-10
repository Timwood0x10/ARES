package observability

import "context"

// MetricsTracer is a Tracer adapter that feeds real LLM/tool calls into the
// Prometheus registry and the cost dashboard. Before it existed, the
// default NoopTracer left every ARES_* counter at
// zero — the /metrics endpoint was wired but永远 empty.
//
// Structure: embeds the Noop tracer for the firehose methods the metrics
// registry does not model (agent steps, errors, trace-id plumbing), and
// overrides the two hot paths (LLM calls, tool calls) with real recording.
type MetricsTracer struct {
	Tracer
	metrics   *PrometheusMetrics
	dashboard *CostDashboard
}

// NewMetricsTracer wraps the metrics registry (and optionally the cost
// dashboard) as a Tracer. Both may be nil — nil metrics discards everything
// (equivalent to the Noop tracer), nil dashboard skips cost attribution.
//
// Args:
//   - metrics: the Prometheus registry to record into.
//   - dashboard: the cost dashboard receiving per-session cost entries.
//
// Returns:
//
//	Tracer - the composite tracer.
func NewMetricsTracer(metrics *PrometheusMetrics, dashboard *CostDashboard) Tracer {
	return &MetricsTracer{
		Tracer:    NewNoopTracer(),
		metrics:   metrics,
		dashboard: dashboard,
	}
}

// RecordLLMCall forwards the call into the Prometheus counters and attributes
// token cost to the per-trace session in the dashboard.
func (t *MetricsTracer) RecordLLMCall(ctx context.Context, call *LLMCall) {
	if t.metrics != nil && call != nil {
		status := "success"
		if call.Error != nil {
			status = "error"
		}
		t.metrics.RecordLLMCall(call.Model, status, call.Duration.Seconds())
		// Cost attribution: session = trace id (the only per-call scope
		// LLMCall carries); tokens × pricing when the dashboard is wired.
		//
		// TraceID derivation: nothing in the production wiring calls
		// Tracer.WithTrace on the request context, so the id stamped by the
		// caller and the one recoverable from ctx are both empty in
		// practice. Dropping the attribution there (the pre-fix behavior)
		// left the cost dashboard permanently empty — derive a per-call id
		// instead, preferring an id already present (call, then context)
		// so future wiring that propagates one wins over the fallback.
		if t.dashboard != nil {
			traceID := call.TraceID
			if traceID == "" {
				traceID = t.GetTraceID(ctx)
			}
			if traceID == "" {
				traceID = generateTraceID()
			}
			tracker := t.dashboard.RegisterSession(traceID)
			if tracker != nil {
				in, out := splitTokenCounts(call.InputTokens, call.OutputTokens, call.TokensUsed)
				tracker.RecordCall(call.Model, in, out)
			}
		}
	}
}

// splitTokenCounts resolves the input/output token split for one call. When
// the provider reported the real split (any non-zero side), it is used
// verbatim; when only a total is known, it is split 50/50 — the historical
// behavior, now a fallback instead of the only option. Negative values clamp
// to zero.
func splitTokenCounts(in, out, total int) (int, int) {
	if in <= 0 && out <= 0 && total > 0 {
		in = total / 2
		out = total - in
	}
	if in < 0 {
		in = 0
	}
	if out < 0 {
		out = 0
	}
	return in, out
}

// RecordToolCall forwards the tool call into the Prometheus counters.
func (t *MetricsTracer) RecordToolCall(ctx context.Context, call *ToolCall) {
	if t.metrics != nil && call != nil {
		status := "success"
		if call.Error != nil {
			status = "error"
		}
		t.metrics.RecordToolCall(call.ToolName, status)
	}
}
