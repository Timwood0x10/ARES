package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// TestGenerateRecordsProviderUsage pins the Generate-path token accounting
// (backlog M7): the provider-reported usage split must be decoded on the
// plain Generate path (all three providers) and survive into the tracer's
// LLMCall. Previously only Chat decoded usage — every Generate run recorded
// zero tokens, so token budgets and cost dashboards saw nothing.
func TestGenerateRecordsProviderUsage(t *testing.T) {
	t.Run("openai_provider_usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":12,"completion_tokens":7}}`))
		}))
		defer server.Close()

		client, err := NewClient(&Config{
			Provider: "openai",
			BaseURL:  server.URL,
			Model:    "test-model",
			APIKey:   "test-key",
			Timeout:  5,
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		tracer := &captureTracer{}
		client.SetTracer(tracer)

		if _, err := client.Generate(context.Background(), "hello"); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if tracer.call == nil {
			t.Fatal("tracer must receive the call")
		}
		if tracer.call.InputTokens != 12 || tracer.call.OutputTokens != 7 {
			t.Fatalf("usage split lost: input=%d output=%d, want 12/7", tracer.call.InputTokens, tracer.call.OutputTokens)
		}
		if tracer.call.TokensUsed != 19 {
			t.Fatalf("TokensUsed = %d, want 19", tracer.call.TokensUsed)
		}
	})

	t.Run("ollama_provider_usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"response":"hi","prompt_eval_count":30,"eval_count":9}`))
		}))
		defer server.Close()

		client, err := NewClient(&Config{
			Provider: "ollama",
			BaseURL:  server.URL,
			Model:    "test-model",
			Timeout:  5,
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		tracer := &captureTracer{}
		client.SetTracer(tracer)

		if _, err := client.Generate(context.Background(), "hello"); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if tracer.call.InputTokens != 30 || tracer.call.OutputTokens != 9 {
			t.Fatalf("usage split lost: input=%d output=%d, want 30/9", tracer.call.InputTokens, tracer.call.OutputTokens)
		}
	})

	t.Run("anthropic_provider_usage", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":8,"output_tokens":3}}`))
		}))
		defer server.Close()

		client, err := NewClient(&Config{
			Provider: "anthropic",
			BaseURL:  server.URL,
			Model:    "test-model",
			APIKey:   "test-key",
			Timeout:  5,
		})
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		tracer := &captureTracer{}
		client.SetTracer(tracer)

		if _, err := client.Generate(context.Background(), "hello"); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if tracer.call.InputTokens != 8 || tracer.call.OutputTokens != 3 {
			t.Fatalf("usage split lost: input=%d output=%d, want 8/3", tracer.call.InputTokens, tracer.call.OutputTokens)
		}
	})
}
