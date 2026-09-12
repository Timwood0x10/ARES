package sdk

import (
	"context"
	"testing"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// TestSubmitConcurrentThroughScheduler verifies the merged path is safe for
// concurrent submits: each submission is an independently admitted L2 session
// completed by the shared scheduler.
func TestSubmitConcurrentThroughScheduler(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "r1"}, {Content: "r2"}, {Content: "r3"},
	}}
	rt.RegisterAgent("coder")

	results := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			_, err := rt.Submit(context.Background(), Task{Capability: "coder", Input: "task"})
			results <- err
		}()
	}
	for i := 0; i < 3; i++ {
		if err := <-results; err != nil {
			t.Fatalf("concurrent submit %d: %v", i, err)
		}
	}
}
