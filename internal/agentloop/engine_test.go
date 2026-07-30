package agentloop

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/api/tools"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/tools/toolsource"
)

// TestDiscoverToolsName_MatchesToolsourceConstant guards the intentionally
// duplicated discover_tools constant: agentloop must not import toolsource, so
// both packages define DiscoverToolsName. If they drift, the engine's
// tool-call dispatch would silently fail to match the meta-tool. This test is
// the single source of truth linking the two copies.
func TestDiscoverToolsName_MatchesToolsourceConstant(t *testing.T) {
	if DiscoverToolsName != toolsource.DiscoverToolsName {
		t.Fatalf("agentloop.DiscoverToolsName=%q != toolsource.DiscoverToolsName=%q",
			DiscoverToolsName, toolsource.DiscoverToolsName)
	}
}

// mockLLM scripts Generate responses per call. When the scripted responses are
// exhausted, the last response is repeated so a tool-call loop can run to the
// max-iterations cap without indexing past the slice.
type mockLLM struct {
	mu        sync.Mutex
	responses []*core.GenerateResponse
	errs      []error
	calls     int
	reqs      []*core.GenerateRequest
}

func (m *mockLLM) Generate(_ context.Context, req *core.GenerateRequest) (*core.GenerateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.calls
	m.calls++
	// Capture the request so tests can assert on Tools/Messages per iteration.
	// The engine builds a fresh GenerateRequest each iteration, so storing the
	// pointer is sufficient; the Tools slice header is fixed at call time.
	m.reqs = append(m.reqs, req)
	if idx < len(m.errs) && m.errs[idx] != nil {
		return nil, m.errs[idx]
	}
	if len(m.responses) == 0 {
		return &core.GenerateResponse{Content: ""}, nil
	}
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	return m.responses[idx], nil
}

// snapshotReqs returns a copy of the captured GenerateRequests in call order.
func (m *mockLLM) snapshotReqs() []*core.GenerateRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*core.GenerateRequest, len(m.reqs))
	copy(out, m.reqs)
	return out
}

// GetProvider returns a fixed provider for friendlyErr hint lookups.
func (m *mockLLM) GetProvider() core.LLMProvider { return core.LLMProviderOllama }

// mockToolExecutor returns canned results by tool name.
type mockToolExecutor struct {
	mu       sync.Mutex
	results  map[string]tools.Result
	errs     map[string]error
	calls    int
	lastName string
}

func (m *mockToolExecutor) Execute(_ context.Context, name string, _ map[string]any) (tools.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastName = name
	if err, ok := m.errs[name]; ok {
		return tools.Result{}, err
	}
	if res, ok := m.results[name]; ok {
		return res, nil
	}
	return tools.Result{Success: true, Data: "default"}, nil
}

// lastToolName returns the name of the most recently executed tool (race-safe).
func (m *mockToolExecutor) lastToolName() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastName
}

// fakeEventSink records every appended event without optimistic-concurrency
// enforcement, so engine emission order and Version fields can be inspected.
type fakeEventSink struct {
	mu     sync.Mutex
	events []*ares_events.Event
}

func (s *fakeEventSink) Append(_ context.Context, _ string, events []*ares_events.Event, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *fakeEventSink) snapshot() []*ares_events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*ares_events.Event, len(s.events))
	copy(out, s.events)
	return out
}

// fakeMemorySink records AddMessage calls.
type fakeMemorySink struct {
	mu       sync.Mutex
	messages []memEntry
}

type memEntry struct {
	sessionID string
	role      string
	content   string
}

func (m *fakeMemorySink) AddMessage(_ context.Context, sessionID, role, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, memEntry{sessionID: sessionID, role: role, content: content})
	return nil
}

func (m *fakeMemorySink) snapshot() []memEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]memEntry, len(m.messages))
	copy(out, m.messages)
	return out
}

// toolCall builds a core.ToolCall with the given name and JSON arguments.
func toolCall(id, name, args string) core.ToolCall {
	return core.ToolCall{
		ID:   id,
		Type: "function",
		Function: core.FunctionCall{
			Name:      name,
			Arguments: args,
		},
	}
}

// TestEngine_Run covers the core loop scenarios: simple answer, tool-then-answer,
// max iterations, and human rejection. Each case scripts the mock LLM and asserts
// on the returned Result.
func TestEngine_Run(t *testing.T) {
	tests := []struct {
		name       string
		responses  []*core.GenerateResponse
		toolResult tools.Result
		human      HumanInputFunc
		maxIter    int
		wantOutput string
		wantCalls  int
	}{
		{
			name: "simple turn returns final answer with no tool calls",
			responses: []*core.GenerateResponse{
				{Content: "hello world", Usage: core.TokenUsage{PromptTokens: 5, CompletionTokens: 3}},
			},
			maxIter:    5,
			wantOutput: "hello world",
			wantCalls:  0,
		},
		{
			name: "tool call then final answer",
			responses: []*core.GenerateResponse{
				{Content: "", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{"x":"1"}`)}},
				{Content: "final answer", Usage: core.TokenUsage{PromptTokens: 2, CompletionTokens: 4}},
			},
			toolResult: tools.Result{Success: true, Data: "42"},
			maxIter:    5,
			wantOutput: "final answer",
			wantCalls:  1,
		},
		{
			name: "max iterations reached when LLM always calls tools",
			responses: []*core.GenerateResponse{
				{Content: "", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{}`)}},
			},
			toolResult: tools.Result{Success: true, Data: "42"},
			maxIter:    3,
			wantOutput: "max iterations reached",
			wantCalls:  3,
		},
		{
			name: "human rejects tool call then loop continues to answer",
			responses: []*core.GenerateResponse{
				{Content: "", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{}`)}},
				{Content: "after rejection"},
			},
			human: func(_ context.Context, _ string, _ map[string]any) (bool, error) {
				return false, nil
			},
			maxIter:    5,
			wantOutput: "after rejection",
			wantCalls:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &mockLLM{responses: tt.responses}
			toolEx := &mockToolExecutor{
				results: map[string]tools.Result{"calc": tt.toolResult},
			}
			eng := &Engine{LLM: llm, Tools: toolEx}

			req := &Request{
				Messages:   []*core.LLMMessage{{Role: "user", Content: "hi"}},
				Tools:      []core.Tool{{Type: "function", Function: core.FunctionDefinition{Name: "calc"}}},
				MaxIter:    tt.maxIter,
				AgentName:  "test-agent",
				SessionID:  "sess-1",
				Input:      "hi",
				HumanInput: tt.human,
			}
			res, err := eng.Run(context.Background(), req)
			if err != nil {
				t.Fatalf("Engine.Run returned error: %v", err)
			}
			if res.Output != tt.wantOutput {
				t.Errorf("Output = %q, want %q", res.Output, tt.wantOutput)
			}
			if res.ToolCalls != tt.wantCalls {
				t.Errorf("ToolCalls = %d, want %d", res.ToolCalls, tt.wantCalls)
			}
		})
	}
}

// TestEngine_LLMErrorWrapped verifies that an LLM error is wrapped with a
// friendly hint via FriendlyErr and aborts the run.
func TestEngine_LLMErrorWrapped(t *testing.T) {
	llm := &mockLLM{
		responses: []*core.GenerateResponse{{Content: "x"}},
		errs:      []error{errors.New("connection refused")},
	}
	eng := &Engine{LLM: llm, Tools: &mockToolExecutor{}}
	req := &Request{
		Messages: []*core.LLMMessage{{Role: "user", Content: "hi"}},
		MaxIter:  3,
	}
	_, err := eng.Run(context.Background(), req)
	if err == nil {
		t.Fatal("expected error from LLM failure")
	}
	if !errors.Is(err, errors.New("connection refused")) {
		// FriendlyErr does not wrap with %w (matches sdk behaviour), so check
		// the message text contains the original error instead.
		if !contains(err.Error(), "connection refused") {
			t.Errorf("error %q does not mention underlying cause", err)
		}
	}
}

// TestEngine_EventsEmitted verifies that tool-call events are emitted in
// Started→Completed order with Version tracking the tool-call count, and that
// TaskCompleted is emitted on the session stream with the task/result payload.
func TestEngine_EventsEmitted(t *testing.T) {
	sink := &fakeEventSink{}
	llm := &mockLLM{responses: []*core.GenerateResponse{
		{Content: "", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{}`)}},
		{Content: "", ToolCalls: []core.ToolCall{toolCall("tc2", "calc", `{}`)}},
		{Content: "final answer"},
	}}
	toolEx := &mockToolExecutor{results: map[string]tools.Result{"calc": {Success: true, Data: "42"}}}
	eng := &Engine{
		LLM:            llm,
		Tools:          toolEx,
		Events:         sink,
		DistillEnabled: true,
	}
	req := &Request{
		Messages:  []*core.LLMMessage{{Role: "user", Content: "hi"}},
		Tools:     []core.Tool{{Type: "function", Function: core.FunctionDefinition{Name: "calc"}}},
		MaxIter:   5,
		AgentName: "agent-x",
		SessionID: "sess-events",
		Input:     "hi",
	}
	res, err := eng.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	if res.Output != "final answer" {
		t.Fatalf("Output = %q, want %q", res.Output, "final answer")
	}

	evs := sink.snapshot()
	// 2 tool calls × (Started + Completed) + 1 TaskCompleted = 5 events.
	wantTypes := []ares_events.EventType{
		ares_events.EventToolCallStarted,
		ares_events.EventToolCallCompleted,
		ares_events.EventToolCallStarted,
		ares_events.EventToolCallCompleted,
		ares_events.EventTaskCompleted,
	}
	if len(evs) != len(wantTypes) {
		t.Fatalf("got %d events, want %d", len(evs), len(wantTypes))
	}
	for i, want := range wantTypes {
		if evs[i].Type != want {
			t.Errorf("event[%d].Type = %s, want %s", i, evs[i].Type, want)
		}
	}
	// Tool-call versions track the tool-call count: 1 for the first call,
	// 2 for the second (increment across calls).
	if evs[0].Version != 1 || evs[1].Version != 1 {
		t.Errorf("first tool-call versions = %d,%d, want 1,1", evs[0].Version, evs[1].Version)
	}
	if evs[2].Version != 2 || evs[3].Version != 2 {
		t.Errorf("second tool-call versions = %d,%d, want 2,2", evs[2].Version, evs[3].Version)
	}
	// Tool-call events stream under the agent name; TaskCompleted under the session.
	for i := 0; i < 4; i++ {
		if evs[i].StreamID != "agent-x" {
			t.Errorf("tool event[%d].StreamID = %q, want %q", i, evs[i].StreamID, "agent-x")
		}
	}
	tc := evs[4]
	if tc.StreamID != "sess-events" {
		t.Errorf("TaskCompleted.StreamID = %q, want %q", tc.StreamID, "sess-events")
	}
	if tc.Type != ares_events.EventTaskCompleted {
		t.Errorf("TaskCompleted.Type = %s", tc.Type)
	}
	if got := tc.Payload[ares_events.EventKeyTask]; got != "hi" {
		t.Errorf("TaskCompleted task = %v, want %q", got, "hi")
	}
	if got := tc.Payload[ares_events.EventKeyResult]; got != "final answer" {
		t.Errorf("TaskCompleted result = %v, want %q", got, "final answer")
	}
	if got := tc.Payload[ares_events.EventKeyTenantID]; got != ares_events.DefaultTenantID {
		t.Errorf("TaskCompleted tenant = %v, want %q", got, ares_events.DefaultTenantID)
	}
	if got := tc.Payload["agent_id"]; got != "agent-x" {
		t.Errorf("TaskCompleted agent_id = %v, want %q", got, "agent-x")
	}
}

// TestEngine_TaskCompletedGatedByDistill verifies that TaskCompleted is NOT
// emitted when DistillEnabled is false (mirrors distillSvc == nil in the sdk),
// while tool-call events are still emitted.
func TestEngine_TaskCompletedGatedByDistill(t *testing.T) {
	sink := &fakeEventSink{}
	llm := &mockLLM{responses: []*core.GenerateResponse{
		{Content: "", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{}`)}},
		{Content: "done"},
	}}
	eng := &Engine{
		LLM:            llm,
		Tools:          &mockToolExecutor{results: map[string]tools.Result{"calc": {Success: true, Data: "x"}}},
		Events:         sink,
		DistillEnabled: false,
	}
	req := &Request{
		Messages:  []*core.LLMMessage{{Role: "user", Content: "hi"}},
		MaxIter:   5,
		AgentName: "a",
		SessionID: "s",
		Input:     "hi",
	}
	if _, err := eng.Run(context.Background(), req); err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	evs := sink.snapshot()
	for _, ev := range evs {
		if ev.Type == ares_events.EventTaskCompleted {
			t.Fatal("TaskCompleted must not be emitted when DistillEnabled is false")
		}
	}
	if len(evs) != 2 {
		t.Errorf("expected 2 tool events (no TaskCompleted), got %d", len(evs))
	}
}

// TestEngine_WithMemory verifies that assistant messages are persisted via
// MemorySink.AddMessage when MemEnabled is true. The user-message AddMessage
// stays in the sdk (buildMessages), so the engine only records assistant turns.
func TestEngine_WithMemory(t *testing.T) {
	mem := &fakeMemorySink{}
	llm := &mockLLM{responses: []*core.GenerateResponse{
		{Content: "thinking", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{}`)}},
		{Content: "final answer"},
	}}
	eng := &Engine{
		LLM:        llm,
		Tools:      &mockToolExecutor{results: map[string]tools.Result{"calc": {Success: true, Data: "42"}}},
		Memory:     mem,
		MemEnabled: true,
	}
	req := &Request{
		Messages:  []*core.LLMMessage{{Role: "user", Content: "hi"}},
		MaxIter:   5,
		AgentName: "a",
		SessionID: "sess-mem",
		Input:     "hi",
	}
	if _, err := eng.Run(context.Background(), req); err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	msgs := mem.snapshot()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 assistant AddMessage calls, got %d", len(msgs))
	}
	for i, m := range msgs {
		if m.role != "assistant" {
			t.Errorf("message[%d].role = %q, want %q", i, m.role, "assistant")
		}
		if m.sessionID != "sess-mem" {
			t.Errorf("message[%d].sessionID = %q, want %q", i, m.sessionID, "sess-mem")
		}
	}
	if msgs[0].content != "thinking" {
		t.Errorf("message[0].content = %q, want %q", msgs[0].content, "thinking")
	}
	if msgs[1].content != "final answer" {
		t.Errorf("message[1].content = %q, want %q", msgs[1].content, "final answer")
	}
}

// TestEngine_MemoryDisabled verifies no AddMessage calls happen when MemEnabled
// is false, even if Memory is set.
func TestEngine_MemoryDisabled(t *testing.T) {
	mem := &fakeMemorySink{}
	llm := &mockLLM{responses: []*core.GenerateResponse{{Content: "done"}}}
	eng := &Engine{
		LLM:        llm,
		Tools:      &mockToolExecutor{},
		Memory:     mem,
		MemEnabled: false,
	}
	req := &Request{
		Messages:  []*core.LLMMessage{{Role: "user", Content: "hi"}},
		MaxIter:   3,
		SessionID: "s",
	}
	if _, err := eng.Run(context.Background(), req); err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	if len(mem.snapshot()) != 0 {
		t.Errorf("expected no AddMessage calls when MemEnabled is false, got %d", len(mem.snapshot()))
	}
}

// TestEngine_HumanInputError verifies that an error from HumanInput aborts the
// run wrapped with context.
func TestEngine_HumanInputError(t *testing.T) {
	llm := &mockLLM{responses: []*core.GenerateResponse{
		{Content: "", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{}`)}},
	}}
	wantErr := errors.New("operator unavailable")
	eng := &Engine{
		LLM:   llm,
		Tools: &mockToolExecutor{},
	}
	req := &Request{
		Messages: []*core.LLMMessage{{Role: "user", Content: "hi"}},
		MaxIter:  3,
		HumanInput: func(_ context.Context, _ string, _ map[string]any) (bool, error) {
			return false, wantErr
		},
	}
	_, err := eng.Run(context.Background(), req)
	if err == nil {
		t.Fatal("expected error from HumanInput failure")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error %q does not wrap %q", err, wantErr)
	}
}

// TestEngine_TokenCounting verifies prompt/completion tokens accumulate across
// all LLM calls.
func TestEngine_TokenCounting(t *testing.T) {
	llm := &mockLLM{responses: []*core.GenerateResponse{
		{Content: "", ToolCalls: []core.ToolCall{toolCall("tc1", "calc", `{}`)},
			Usage: core.TokenUsage{PromptTokens: 10, CompletionTokens: 2}},
		{Content: "done", Usage: core.TokenUsage{PromptTokens: 20, CompletionTokens: 5}},
	}}
	eng := &Engine{
		LLM:   llm,
		Tools: &mockToolExecutor{results: map[string]tools.Result{"calc": {Success: true, Data: "x"}}},
	}
	req := &Request{
		Messages: []*core.LLMMessage{{Role: "user", Content: "hi"}},
		MaxIter:  5,
	}
	res, err := eng.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	if res.InputTokens != 30 {
		t.Errorf("InputTokens = %d, want 30", res.InputTokens)
	}
	if res.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", res.OutputTokens)
	}
}

// contains is a minimal strings.Contains to avoid importing strings for one use.
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// fakeToolExpander is a test ToolExpander that resolves requested names from a
// pre-built map. Unknown names are skipped (not an error). It records every
// Expand call so tests can assert on the names seen.
type fakeToolExpander struct {
	mu    sync.Mutex
	calls int
	seen  [][]string
	tools map[string]core.Tool
	err   error
}

func (f *fakeToolExpander) Expand(_ context.Context, names []string) ([]core.Tool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	cp := make([]string, len(names))
	copy(cp, names)
	f.seen = append(f.seen, cp)
	if f.err != nil {
		return nil, f.err
	}
	out := make([]core.Tool, 0, len(names))
	for _, n := range names {
		if t, ok := f.tools[n]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeToolExpander) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// toolListCount returns the number of tools with the given Function.Name.
func toolListCount(tools []core.Tool, name string) int {
	n := 0
	for _, t := range tools {
		if t.Function.Name == name {
			n++
		}
	}
	return n
}

// findToolMessage searches messages for the first tool-role message whose
// content contains sub. Used to confirm a discover_tools result was appended.
func findToolMessage(msgs []*core.LLMMessage, sub string) (*core.LLMMessage, bool) {
	for _, m := range msgs {
		if m.Role == roleTool && contains(m.Content, sub) {
			return m, true
		}
	}
	return nil, false
}

// TestEngine_DiscoverToolsExpansion verifies the full discovery flow: on iter 0
// the LLM calls discover_tools, the engine expands the returned names into LLM
// tool defs, and those defs are present in the GenerateRequest.Tools of iter 1
// (alongside the original tools). On iter 1 the LLM calls one of the expanded
// tools, which must be dispatched through ToolExecutor.
func TestEngine_DiscoverToolsExpansion(t *testing.T) {
	// discoverResult is the JSON array of {name, description} objects the
	// discover_tools tool returns.
	const discoverResult = `[{"name":"search","description":"search the web"},{"name":"translate","description":"translate text"}]`
	expanded := map[string]core.Tool{
		"search":    {Type: "function", Function: core.FunctionDefinition{Name: "search", Description: "search the web"}},
		"translate": {Type: "function", Function: core.FunctionDefinition{Name: "translate", Description: "translate text"}},
	}
	expander := &fakeToolExpander{tools: expanded}

	llm := &mockLLM{responses: []*core.GenerateResponse{
		// iter 0: call the discover_tools meta-tool.
		{Content: "", ToolCalls: []core.ToolCall{toolCall("d1", DiscoverToolsName, `{"query":"go"}`)}},
		// iter 1: call one of the EXPANDED tools.
		{Content: "", ToolCalls: []core.ToolCall{toolCall("s1", "search", `{"q":"go"}`)}},
		// iter 2: final answer.
		{Content: "final answer"},
	}}
	toolEx := &mockToolExecutor{results: map[string]tools.Result{
		DiscoverToolsName: {Success: true, Data: discoverResult},
		"search":          {Success: true, Data: "search-result"},
	}}
	eng := &Engine{LLM: llm, Tools: toolEx}
	req := &Request{
		Messages:     []*core.LLMMessage{{Role: "user", Content: "hi"}},
		Tools:        []core.Tool{{Type: "function", Function: core.FunctionDefinition{Name: "calc"}}},
		MaxIter:      5,
		AgentName:    "discover-agent",
		ToolExpander: expander,
	}

	res, err := eng.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	if res.Output != "final answer" {
		t.Errorf("Output = %q, want %q", res.Output, "final answer")
	}
	if res.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want 2 (discover_tools + search)", res.ToolCalls)
	}

	reqs := llm.snapshotReqs()
	if len(reqs) != 3 {
		t.Fatalf("expected 3 LLM calls, got %d", len(reqs))
	}
	// iter 0 saw only the original tool set.
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Function.Name != "calc" {
		t.Errorf("iter 0 tools = %+v, want only [calc]", reqs[0].Tools)
	}
	// iter 1 (after expansion) must include the expanded tools AND the original.
	iter1 := reqs[1].Tools
	if !toolNameInSet("calc", iter1) {
		t.Errorf("iter 1 tools missing original 'calc': %+v", iter1)
	}
	if !toolNameInSet("search", iter1) {
		t.Errorf("iter 1 tools missing expanded 'search': %+v", iter1)
	}
	if !toolNameInSet("translate", iter1) {
		t.Errorf("iter 1 tools missing expanded 'translate': %+v", iter1)
	}
	if got := toolListCount(iter1, "search"); got != 1 {
		t.Errorf("iter 1 'search' count = %d, want 1", got)
	}

	// The expanded tool's call was dispatched through ToolExecutor.
	if got := toolEx.lastToolName(); got != "search" {
		t.Errorf("last executed tool = %q, want %q", got, "search")
	}
	if got := expander.callCount(); got != 1 {
		t.Errorf("expander call count = %d, want 1", got)
	}
}

// TestEngine_DiscoverToolsDedup verifies that calling discover_tools twice with
// the same names does not duplicate entries in the active tool set.
func TestEngine_DiscoverToolsDedup(t *testing.T) {
	const discoverResult = `[{"name":"search","description":"s"}]`
	expander := &fakeToolExpander{tools: map[string]core.Tool{
		"search": {Type: "function", Function: core.FunctionDefinition{Name: "search"}},
	}}
	llm := &mockLLM{responses: []*core.GenerateResponse{
		// iter 0: discover tools (adds 'search').
		{Content: "", ToolCalls: []core.ToolCall{toolCall("d1", DiscoverToolsName, `{}`)}},
		// iter 1: discover tools AGAIN with the same name.
		{Content: "", ToolCalls: []core.ToolCall{toolCall("d2", DiscoverToolsName, `{}`)}},
		// iter 2: final answer.
		{Content: "done"},
	}}
	toolEx := &mockToolExecutor{results: map[string]tools.Result{
		DiscoverToolsName: {Success: true, Data: discoverResult},
	}}
	eng := &Engine{LLM: llm, Tools: toolEx}
	req := &Request{
		Messages:     []*core.LLMMessage{{Role: "user", Content: "hi"}},
		Tools:        []core.Tool{{Type: "function", Function: core.FunctionDefinition{Name: "calc"}}},
		MaxIter:      5,
		ToolExpander: expander,
	}
	if _, err := eng.Run(context.Background(), req); err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	if got := expander.callCount(); got != 2 {
		t.Errorf("expander call count = %d, want 2", got)
	}
	reqs := llm.snapshotReqs()
	// After two discover_tools calls with the same name, 'search' must appear
	// exactly once in the final active set.
	final := reqs[len(reqs)-1].Tools
	if got := toolListCount(final, "search"); got != 1 {
		t.Errorf("'search' appears %d times in final tool set, want 1: %+v", got, final)
	}
	if got := toolListCount(final, "calc"); got != 1 {
		t.Errorf("'calc' appears %d times in final tool set, want 1: %+v", got, final)
	}
	if len(final) != 2 {
		t.Errorf("final tool set size = %d, want 2 (calc + search)", len(final))
	}
}

// TestEngine_DiscoverToolsNilExpander verifies that when ToolExpander is nil, a
// discover_tools call still completes (its result is appended as a tool message)
// and the active tool set is unchanged.
func TestEngine_DiscoverToolsNilExpander(t *testing.T) {
	const discoverResult = `[{"name":"search","description":"s"},{"name":"translate","description":"t"}]`
	llm := &mockLLM{responses: []*core.GenerateResponse{
		{Content: "", ToolCalls: []core.ToolCall{toolCall("d1", DiscoverToolsName, `{}`)}},
		{Content: "done"},
	}}
	toolEx := &mockToolExecutor{results: map[string]tools.Result{
		DiscoverToolsName: {Success: true, Data: discoverResult},
	}}
	eng := &Engine{LLM: llm, Tools: toolEx}
	req := &Request{
		Messages:     []*core.LLMMessage{{Role: "user", Content: "hi"}},
		Tools:        []core.Tool{{Type: "function", Function: core.FunctionDefinition{Name: "calc"}}},
		MaxIter:      5,
		ToolExpander: nil, // discovery expansion disabled
	}
	res, err := eng.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	if res.Output != "done" {
		t.Errorf("Output = %q, want %q", res.Output, "done")
	}
	reqs := llm.snapshotReqs()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(reqs))
	}
	// iter 1 tools must equal the original set (no expansion happened).
	iter1 := reqs[1].Tools
	if len(iter1) != 1 || iter1[0].Function.Name != "calc" {
		t.Errorf("iter 1 tools = %+v, want only [calc]", iter1)
	}
	// The discover_tools result was still appended as a tool message for the LLM.
	if _, ok := findToolMessage(reqs[1].Messages, discoverResult); !ok {
		t.Errorf("missing tool message with discover_tools result %q", discoverResult)
	}
}

// TestEngine_NoDiscoverTools_BackwardCompat verifies that a normal run with no
// discover_tools call behaves exactly as before: every LLM call sees the
// original tool set, req.Tools is not mutated, and the result is unchanged.
func TestEngine_NoDiscoverTools_BackwardCompat(t *testing.T) {
	llm := &mockLLM{responses: []*core.GenerateResponse{
		{Content: "", ToolCalls: []core.ToolCall{toolCall("c1", "calc", `{}`)}},
		{Content: "done"},
	}}
	toolEx := &mockToolExecutor{results: map[string]tools.Result{"calc": {Success: true, Data: "42"}}}
	eng := &Engine{LLM: llm, Tools: toolEx}
	baseTools := []core.Tool{{Type: "function", Function: core.FunctionDefinition{Name: "calc"}}}
	req := &Request{
		Messages: []*core.LLMMessage{{Role: "user", Content: "hi"}},
		Tools:    baseTools,
		MaxIter:  5,
		// ToolExpander left nil: even if a discover_tools call happened, no
		// expansion would occur. Here no such call happens at all.
	}
	res, err := eng.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("Engine.Run error: %v", err)
	}
	if res.Output != "done" {
		t.Errorf("Output = %q, want %q", res.Output, "done")
	}
	if res.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", res.ToolCalls)
	}
	reqs := llm.snapshotReqs()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(reqs))
	}
	for i, r := range reqs {
		if len(r.Tools) != 1 || r.Tools[0].Function.Name != "calc" {
			t.Errorf("iter %d tools = %+v, want only [calc]", i, r.Tools)
		}
	}
	// The caller's req.Tools slice must not be mutated by the engine.
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "calc" {
		t.Errorf("req.Tools mutated: %+v", req.Tools)
	}
}
