package openaiapi

import (
	"context"
	"encoding/json"
	"testing"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// fakeService is a minimal llmcore.LLMService returning a canned completion.
type fakeService struct{}

func (f *fakeService) Generate(_ context.Context, _ *llmcore.GenerateRequest) (*llmcore.GenerateResponse, error) {
	return &llmcore.GenerateResponse{Content: "hi"}, nil
}

func (f *fakeService) GenerateSimple(_ context.Context, _ string) (string, error) {
	return "hi", nil
}

func (f *fakeService) GenerateEmbedding(_ context.Context, _ *llmcore.EmbeddingRequest) (*llmcore.EmbeddingResponse, error) {
	return &llmcore.EmbeddingResponse{}, nil
}

func (f *fakeService) GetConfig() *llmcore.LLMConfig { return nil }
func (f *fakeService) IsEnabled() bool               { return true }
func (f *fakeService) GetProvider() llmcore.LLMProvider {
	return llmcore.LLMProvider("fake")
}
func (f *fakeService) GetModel() string { return "test-model" }

// TestDetectEndpoint_InputRouting is the #44 regression: an array `input` is
// legal for BOTH the Embeddings API (batch) and the Responses API (multi-turn
// chat). The previous code routed every array input to embeddings, so a
// Responses chat request with multi-turn input produced a meaningless
// embedding instead of a completion. Array input must route by model prefix:
// only text-embedding-* models go to embeddings; everything else is Responses
// (chat).
func TestDetectEndpoint_InputRouting(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "array input, chat model -> responses",
			body: `{"model":"gpt-4o","input":[{"role":"user","content":"hi"}]}`,
			want: responsesEndpoint,
		},
		{
			name: "array input, embedding model -> embeddings",
			body: `{"model":"text-embedding-3-small","input":["a","b"]}`,
			want: embeddingsEndpoint,
		},
		{
			name: "string input, chat model -> responses",
			body: `{"model":"gpt-4o","input":"hi"}`,
			want: responsesEndpoint,
		},
		{
			name: "string input, embedding model -> embeddings",
			body: `{"model":"text-embedding-3-small","input":"hi"}`,
			want: embeddingsEndpoint,
		},
		{
			name: "input with instructions -> responses",
			body: `{"model":"gpt-4o","input":"hi","instructions":"be brief"}`,
			want: responsesEndpoint,
		},
		{
			name: "messages -> chat completions",
			body: `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`,
			want: defaultEndpoint,
		},
		{
			name: "prompt -> legacy completions",
			body: `{"model":"gpt-4o","prompt":"hi"}`,
			want: "completions",
		},
		{
			name: "no discriminators -> chat completions",
			body: `{"model":"gpt-4o"}`,
			want: defaultEndpoint,
		},
		{
			name: "invalid json -> chat completions",
			body: `not json`,
			want: defaultEndpoint,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectEndpoint([]byte(tt.body)); got != tt.want {
				t.Errorf("detectEndpoint(%s) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

// TestDetectEndpoint_ArrayInputProducesChatResponse locks the end-to-end
// consequence: a Responses-style array input on a chat model must be answered
// with a Responses API response object ("response"), never an embedding list.
func TestDetectEndpoint_ArrayInputProducesChatResponse(t *testing.T) {
	raw := []byte(`{"model":"test-model","input":[{"role":"user","content":"hello"}]}`)
	ep := detectEndpoint(raw)
	if ep != responsesEndpoint {
		t.Fatalf("array chat input routed to %q, want %q", ep, responsesEndpoint)
	}
	// The responses handler shape check: its response object is "response".
	var svc fakeService
	a, err := New(map[string]any{"service": &svc})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := a.Serve(t.Context(), raw)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var env struct {
		Object string `json:"object"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if env.Object != "response" {
		t.Errorf("response object = %q, want \"response\" (Responses API chat shape)", env.Object)
	}
}
