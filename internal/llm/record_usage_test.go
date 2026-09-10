package llm

import (
	"context"
	"testing"
	"time"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	"github.com/Timwood0x10/ares/internal/runtime/observability"
)

// captureTracer records the LLMCall handed to RecordLLMCall.
type captureTracer struct {
	observability.NoopTracer
	call *observability.LLMCall
}

func (c *captureTracer) RecordLLMCall(_ context.Context, call *observability.LLMCall) {
	c.call = call
}

// TestRecordLLMCallCarriesUsageSplit pins the plumbing half of HIGH #14: the
// provider-reported input/output token split must survive recordLLMCall into
// the tracer's LLMCall (so MetricsTracer can attribute real cost instead of
// halving the total), and a total-only usage still flows through with the
// split left zero.
func TestRecordLLMCallCarriesUsageSplit(t *testing.T) {
	client, err := NewClient(&Config{
		Provider: string(ProviderOpenAI),
		APIKey:   "test-key",
		BaseURL:  "http://localhost:1",
		Model:    "gpt-4o",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	tracer := &captureTracer{}
	client.SetTracer(tracer)

	client.recordLLMCall(context.Background(), "p", "r", llmcore.TokenUsage{PromptTokens: 70, CompletionTokens: 20, TotalTokens: 90}, time.Now(), nil)
	if tracer.call == nil {
		t.Fatal("tracer must receive the call")
	}
	if tracer.call.InputTokens != 70 || tracer.call.OutputTokens != 20 {
		t.Fatalf("usage split lost: input=%d output=%d, want 70/20", tracer.call.InputTokens, tracer.call.OutputTokens)
	}
	if tracer.call.TokensUsed != 90 {
		t.Fatalf("TokensUsed = %d, want 90", tracer.call.TokensUsed)
	}

	// Total-only usage (e.g. a provider that reports no split): the split
	// stays zero and the total flows through.
	tracer.call = nil
	client.recordLLMCall(context.Background(), "p", "r", llmcore.TokenUsage{TotalTokens: 42}, time.Now(), nil)
	if tracer.call.TokensUsed != 42 {
		t.Fatalf("TokensUsed = %d, want 42", tracer.call.TokensUsed)
	}
	if tracer.call.InputTokens != 0 || tracer.call.OutputTokens != 0 {
		t.Fatalf("total-only usage must leave the split zero, got %d/%d", tracer.call.InputTokens, tracer.call.OutputTokens)
	}

	// Split-only usage (total missing): the total is derived as the sum.
	tracer.call = nil
	client.recordLLMCall(context.Background(), "p", "r", llmcore.TokenUsage{PromptTokens: 11, CompletionTokens: 4}, time.Now(), nil)
	if tracer.call.TokensUsed != 15 {
		t.Fatalf("derived TokensUsed = %d, want 15", tracer.call.TokensUsed)
	}
}
