package observability

import (
	"context"
	"testing"
)

// The DEEP_CODE_REVIEW_2026 HIGH regressions #13 and #14: MetricsTracer must
// attribute cost even when no trace id was ever injected (WithTrace is not
// called anywhere in the production wiring — the pre-fix `call.TraceID != ""`
// guard left the cost dashboard permanently empty), and the input/output
// token split must use the provider-reported values instead of a hardcoded
// 50/50.

func newTestMetricsTracer(t *testing.T) (*MetricsTracer, *CostDashboard) {
	t.Helper()
	metrics, err := NewPrometheusMetrics()
	if err != nil {
		t.Fatalf("NewPrometheusMetrics: %v", err)
	}
	dashboard := NewCostDashboard()
	tracer := NewMetricsTracer(metrics, dashboard).(*MetricsTracer)
	return tracer, dashboard
}

// TestMetricsTracerAttributesCostWithoutTraceID pins #13: a call with an
// empty TraceID (the production default — nobody calls WithTrace) still
// lands in the dashboard instead of being dropped.
func TestMetricsTracerAttributesCostWithoutTraceID(t *testing.T) {
	tracer, dashboard := newTestMetricsTracer(t)

	tracer.RecordLLMCall(context.Background(), &LLMCall{
		Model:      "gpt-4o",
		TokensUsed: 90,
	})

	sessions := dashboard.GetAllSessions()
	if len(sessions) != 1 {
		t.Fatalf("cost attribution with no trace id must still register a session, got %d sessions", len(sessions))
	}
	report, ok := dashboard.GetSessionCost(sessions[0].SessionID)
	if !ok {
		t.Fatalf("session %q must be queryable", sessions[0].SessionID)
	}
	if report.CallCount != 1 {
		t.Fatalf("CallCount = %d, want 1", report.CallCount)
	}
}

// TestMetricsTracerPrefersExplicitTraceID pins the precedence chain of #13:
// an id already on the call wins; otherwise one recoverable from the context
// (future wiring) wins; only then is a per-call id generated.
func TestMetricsTracerPrefersExplicitTraceID(t *testing.T) {
	tracer, dashboard := newTestMetricsTracer(t)

	// Explicit id on the call.
	tracer.RecordLLMCall(context.Background(), &LLMCall{TraceID: "explicit-1", TokensUsed: 10})
	// Id in the context (via the embedded Noop tracer's WithTrace).
	ctx := NewNoopTracer().WithTrace(context.Background())
	tracer.RecordLLMCall(ctx, &LLMCall{TokensUsed: 10})
	// Nothing anywhere: generated.
	tracer.RecordLLMCall(context.Background(), &LLMCall{TokensUsed: 10})

	sessions := dashboard.GetAllSessions()
	if len(sessions) != 3 {
		t.Fatalf("three distinct sessions expected, got %d", len(sessions))
	}
	if sessions[0].SessionID != "explicit-1" {
		t.Errorf("first session = %q, want explicit-1", sessions[0].SessionID)
	}
	// The context-carried id is a non-empty generated one and is reused for
	// the second call only.
	if sessions[1].SessionID == "" || sessions[1].SessionID == "explicit-1" {
		t.Errorf("second session = %q, want the context trace id", sessions[1].SessionID)
	}
	// Same context id must not collide with the third (generated) one.
	if sessions[2].SessionID == sessions[1].SessionID {
		t.Errorf("third session must be a fresh generated id, duplicated %q", sessions[2].SessionID)
	}
}

// TestMetricsTracerUsesReportedTokenSplit pins #14: when the provider
// reported the input/output split, the dashboard records those exact values —
// not a 50/50 halving of the total.
func TestMetricsTracerUsesReportedTokenSplit(t *testing.T) {
	tracer, dashboard := newTestMetricsTracer(t)

	tracer.RecordLLMCall(context.Background(), &LLMCall{
		TraceID:      "split-1",
		Model:        "gpt-4o",
		TokensUsed:   90,
		InputTokens:  70,
		OutputTokens: 20,
	})

	report, ok := dashboard.GetSessionCost("split-1")
	if !ok {
		t.Fatal("session split-1 must exist")
	}
	if report.TotalInput != 70 {
		t.Errorf("TotalInput = %d, want 70 (reported value, not total/2)", report.TotalInput)
	}
	if report.TotalOutput != 20 {
		t.Errorf("TotalOutput = %d, want 20 (reported value, not total/2)", report.TotalOutput)
	}
}

// TestMetricsTracerFallsBackToFiftyFiftySplit pins the fallback half of #14:
// when only a total is known (legacy callers, providers without a split),
// the total is halved — the historical behavior.
func TestMetricsTracerFallsBackToFiftyFiftySplit(t *testing.T) {
	tracer, dashboard := newTestMetricsTracer(t)

	tracer.RecordLLMCall(context.Background(), &LLMCall{
		TraceID:    "fifty-1",
		Model:      "gpt-4o",
		TokensUsed: 101,
	})

	report, ok := dashboard.GetSessionCost("fifty-1")
	if !ok {
		t.Fatal("session fifty-1 must exist")
	}
	if report.TotalInput != 50 {
		t.Errorf("TotalInput = %d, want 50 (101/2)", report.TotalInput)
	}
	if report.TotalOutput != 51 {
		t.Errorf("TotalOutput = %d, want 51 (odd remainder to output)", report.TotalOutput)
	}
}
