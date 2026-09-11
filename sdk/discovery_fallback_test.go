package sdk

import (
	"context"
	"errors"
	"testing"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	rescore "github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/Timwood0x10/ares/internal/tools/toolsource"
)

// failingSource is a ToolSource whose Tools always errors, forcing the
// discovery fallback path in resolveTools.
type failingSource struct{}

func (failingSource) Tools(context.Context) ([]rescore.Tool, error) {
	return nil, errors.New("source unavailable")
}

func (failingSource) OnChange(func()) {}

func (failingSource) Source() string { return "failing" }

var _ toolsource.ToolSource = failingSource{}

// TestAgent_DiscoverySourceFails_KeepsSyscallTools is the #P0-15 regression:
// when the discovery source failed, the fallback returned a bare tool
// snapshot WITHOUT the runtime's syscall tools (spawn_agent/create_task),
// contradicting resolveTools' contract that every fallback path appends
// them — the agent could no longer decompose or spawn work.
func TestAgent_DiscoverySourceFails_KeepsSyscallTools(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()

	// Force the syscall wiring (normally lazy on first Submit) so
	// syscallTools is populated before Run resolves tools. ensureScheduler
	// builds the fabric + scheduler wireSyscalls requires.
	rt.ensureScheduler()
	if len(rt.syscallTools) == 0 {
		t.Fatal("precondition: syscallTools must be populated after ensureScheduler")
	}

	llm := &captureLLMSvc{responses: []*llmcore.GenerateResponse{{Content: "done"}}}
	rt.llmSvc = llm

	agent := rt.NewAgent("fallback-agent", WithToolDiscovery(), WithToolSource(failingSource{}))
	if _, err := agent.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Agent.Run error: %v", err)
	}
	reqs := llm.snapshotReqs()
	if len(reqs) == 0 {
		t.Fatal("expected at least 1 LLM call")
	}
	names := toolNamesIn(reqs[0].Tools)
	for _, want := range []string{"spawn_agent", "create_task"} {
		if !names[want] {
			t.Errorf("fallback path lost syscall tool %q; tools: %v", want, names)
		}
	}
}
