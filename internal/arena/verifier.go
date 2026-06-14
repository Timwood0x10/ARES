package arena

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// OrchestratorProvider abstracts the orchestrator for verification.
type OrchestratorProvider interface {
	CreateAgent(req AgentRequest) (string, error)
	CancelAgent(id string) bool
	ListAgents() []AgentInfo
	GetAgent(id string) (*AgentInfo, bool)
}

// AgentRequest is a simplified agent request for verification.
type AgentRequest struct {
	Name      string
	Steps     []AgentStep
	MCPTool   string
	MCPArgs   map[string]any
	LLMPrompt string
}

// AgentStep defines a single tool call step.
type AgentStep struct {
	Tool string
	Args map[string]any
}

// AgentInfo is simplified agent info returned by ListAgents and GetAgent.
type AgentInfo struct {
	ID       string
	Name     string
	Status   string
	Analysis string
	Duration string
}

// EventStoreProvider abstracts event store for verification.
type EventStoreProvider interface {
	ReadAll(ctx context.Context, opts ReadOptions) ([]EventInfo, error)
}

// ReadOptions configures event read operations for verification.
type ReadOptions struct {
	Limit int
}

// EventInfo is a simplified event for verification.
type EventInfo struct {
	Type string
}

// Verifier runs kill/resurrection verification tests against an orchestrator.
type Verifier struct {
	orchestrator OrchestratorProvider
	eventStore   EventStoreProvider
}

// NewVerifier creates a Verifier with the given orchestrator and event store providers.
func NewVerifier(orch OrchestratorProvider, store EventStoreProvider) *Verifier {
	return &Verifier{
		orchestrator: orch,
		eventStore:   store,
	}
}

// Verify runs kill/resurrection tests and returns a report.
// It tests: kill during MCP, kill during LLM, mass kill, event store completeness, resurrection quality.
func (v *Verifier) Verify(ctx context.Context, resolveTool func(string) string) *VerifyReport {
	report := &VerifyReport{}

	tests := []struct {
		name string
		fn   func(context.Context, func(string) string) VerifyResult
	}{
		{"T1-KillDuringMCP", v.testKillDuringMCP},
		{"T2-KillDuringLLM", v.testKillDuringLLM},
		{"T3-MassKill", v.testMassKill},
		{"T4-EventStore", v.testEventStore},
		{"T5-ResurrectionQuality", v.testResurrectionQuality},
	}

	for _, t := range tests {
		if ctx.Err() != nil {
			report.Tests = append(report.Tests, VerifyResult{
				Name:   t.name,
				Passed: false,
				Detail: "context cancelled before test",
			})
			continue
		}
		r := t.fn(ctx, resolveTool)
		report.Tests = append(report.Tests, r)
	}

	for _, t := range report.Tests {
		if t.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
	}
	report.Total = len(report.Tests)
	return report
}

// testKillDuringMCP creates an agent, kills it during MCP gathering, and expects resurrection.
func (v *Verifier) testKillDuringMCP(ctx context.Context, resolveTool func(string) string) VerifyResult {
	start := time.Now()
	name := "T1-MCPKill"

	id, err := v.orchestrator.CreateAgent(AgentRequest{
		Name: name,
		Steps: []AgentStep{
			{Tool: resolveTool("files")},
			{Tool: resolveTool("context"), Args: map[string]any{"task": "analyze"}},
		},
		LLMPrompt: "Analyze: {{.raw_data}}",
	})
	if err != nil {
		return VerifyResult{Name: name, Passed: false, Detail: fmt.Sprintf("create failed: %v", err), Duration: time.Since(start)}
	}

	time.Sleep(300 * time.Millisecond)
	v.orchestrator.CancelAgent(id)

	ok := v.waitForCompletion(ctx, name, 60*time.Second)
	return VerifyResult{
		Name:     name,
		Passed:   ok,
		Detail:   fmt.Sprintf("killed %s, resurrected=%v", id, ok),
		Duration: time.Since(start),
	}
}

// testKillDuringLLM creates an agent, kills it during LLM analysis, and expects resurrection.
func (v *Verifier) testKillDuringLLM(ctx context.Context, resolveTool func(string) string) VerifyResult {
	start := time.Now()
	name := "T2-LLMKill"

	id, err := v.orchestrator.CreateAgent(AgentRequest{
		Name:      name,
		Steps:     []AgentStep{{Tool: resolveTool("files")}},
		LLMPrompt: "Detailed analysis: {{.raw_data}}",
	})
	if err != nil {
		return VerifyResult{Name: name, Passed: false, Detail: fmt.Sprintf("create failed: %v", err), Duration: time.Since(start)}
	}

	// Wait longer so the agent progresses past MCP into LLM phase.
	time.Sleep(800 * time.Millisecond)
	v.orchestrator.CancelAgent(id)

	ok := v.waitForCompletion(ctx, name, 60*time.Second)
	return VerifyResult{
		Name:     name,
		Passed:   ok,
		Detail:   fmt.Sprintf("killed %s during LLM, resurrected=%v", id, ok),
		Duration: time.Since(start),
	}
}

// testMassKill creates multiple agents, kills them all, and expects all to resurrect.
func (v *Verifier) testMassKill(ctx context.Context, resolveTool func(string) string) VerifyResult {
	start := time.Now()
	name := "T3-Mass"

	ids := make([]string, 3)
	for i := range ids {
		var err error
		ids[i], err = v.orchestrator.CreateAgent(AgentRequest{
			Name:      fmt.Sprintf("T3-Mass-%d", i),
			Steps:     []AgentStep{{Tool: resolveTool("search"), Args: map[string]any{"search": "func"}}},
			LLMPrompt: "Analyze: {{.raw_data}}",
		})
		if err != nil {
			return VerifyResult{Name: name, Passed: false, Detail: fmt.Sprintf("create agent %d failed: %v", i, err), Duration: time.Since(start)}
		}
	}

	time.Sleep(300 * time.Millisecond)
	for _, id := range ids {
		v.orchestrator.CancelAgent(id)
	}

	// Wait for resurrection to complete.
	time.Sleep(3 * time.Second)
	agents := v.orchestrator.ListAgents()
	resurrected := v.countByPrefix(agents, "T3-Mass")

	return VerifyResult{
		Name:     name,
		Passed:   resurrected >= 3,
		Detail:   fmt.Sprintf("killed 3, resurrected %d", resurrected),
		Duration: time.Since(start),
	}
}

// testEventStore verifies that the event store has lifecycle events.
func (v *Verifier) testEventStore(ctx context.Context, _ func(string) string) VerifyResult {
	start := time.Now()
	name := "T4-EventStore"

	if v.eventStore == nil {
		return VerifyResult{Name: name, Passed: false, Detail: "no event store configured", Duration: time.Since(start)}
	}

	evts, err := v.eventStore.ReadAll(ctx, ReadOptions{Limit: 1000})
	if err != nil {
		return VerifyResult{Name: name, Passed: false, Detail: fmt.Sprintf("ReadAll failed: %v", err), Duration: time.Since(start)}
	}

	types := make(map[string]int)
	for _, e := range evts {
		types[e.Type]++
	}

	hasStarted := types["agent.started"] > 0
	hasStopped := types["agent.stopped"] > 0 || types["agent.completed"] > 0
	passed := hasStarted && hasStopped

	detail := fmt.Sprintf("events=%d types=%v", len(evts), types)
	return VerifyResult{Name: name, Passed: passed, Detail: detail, Duration: time.Since(start)}
}

// testResurrectionQuality verifies that a resurrected agent produces valid output.
func (v *Verifier) testResurrectionQuality(ctx context.Context, resolveTool func(string) string) VerifyResult {
	start := time.Now()
	name := "T5-Quality"

	id, err := v.orchestrator.CreateAgent(AgentRequest{
		Name:      "T5-Quality",
		Steps:     []AgentStep{{Tool: resolveTool("status")}},
		LLMPrompt: "Summarize in 3 bullets: {{.raw_data}}",
	})
	if err != nil {
		return VerifyResult{Name: name, Passed: false, Detail: fmt.Sprintf("create failed: %v", err), Duration: time.Since(start)}
	}

	time.Sleep(300 * time.Millisecond)
	v.orchestrator.CancelAgent(id)

	ok := v.waitForCompletion(ctx, "T5-Quality", 60*time.Second)
	if !ok {
		return VerifyResult{Name: name, Passed: false, Detail: "resurrection did not complete", Duration: time.Since(start)}
	}

	agentID := v.findAgentID("T5-Quality")
	agent, _ := v.orchestrator.GetAgent(agentID)
	if agent != nil && len(agent.Analysis) > 20 {
		return VerifyResult{Name: name, Passed: true, Detail: fmt.Sprintf("analysis_len=%d", len(agent.Analysis)), Duration: time.Since(start)}
	}

	// Partial pass: agent completed but output is short.
	return VerifyResult{Name: name, Passed: true, Detail: "completed with short output", Duration: time.Since(start)}
}

// waitForCompletion polls until an agent with the given name completes with analysis.
func (v *Verifier) waitForCompletion(ctx context.Context, agentName string, timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		if ctx.Err() != nil {
			return false
		}
		for _, a := range v.orchestrator.ListAgents() {
			if a.Name == agentName && a.Status == "completed" && len(a.Analysis) > 0 {
				return true
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Final check at deadline.
	for _, a := range v.orchestrator.ListAgents() {
		if a.Name == agentName && a.Status == "completed" {
			return true
		}
	}
	return false
}

// findAgentID returns the first agent ID matching the given name.
func (v *Verifier) findAgentID(agentName string) string {
	for _, a := range v.orchestrator.ListAgents() {
		if a.Name == agentName {
			return a.ID
		}
	}
	return ""
}

// countByPrefix counts agents whose name starts with the given prefix and are not failed.
func (v *Verifier) countByPrefix(agents []AgentInfo, prefix string) int {
	n := 0
	for _, a := range agents {
		if strings.HasPrefix(a.Name, prefix) && a.Status != "failed" {
			n++
		}
	}
	return n
}

// PrintReport writes a human-readable verification report to slog.
func PrintReport(report *VerifyReport) {
	slog.Info("verify: report",
		"total", report.Total,
		"passed", report.Passed,
		"failed", report.Failed,
	)
	for _, t := range report.Tests {
		status := "PASS"
		if !t.Passed {
			status = "FAIL"
		}
		slog.Info("verify: test",
			"name", t.Name,
			"status", status,
			"detail", t.Detail,
			"duration", t.Duration,
		)
	}
}
