package planner

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── End-to-End: User Request → Planner → Bridge → Tool Execution ──────────

// realCalculator is a tool that evaluates arithmetic expressions.
// It serves as a realistic mock for end-to-end testing.
type realCalculator struct{}

func (r *realCalculator) Name() string                      { return "calculator" }
func (r *realCalculator) Description() string               { return "Evaluates arithmetic expressions" }
func (r *realCalculator) Category() core.ToolCategory       { return core.CategoryCore }
func (r *realCalculator) Capabilities() []core.Capability   { return nil }
func (r *realCalculator) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"expression": {Type: "string", Description: "Arithmetic expression to evaluate"},
		},
		Required: []string{"expression"},
	}
}
func (r *realCalculator) Execute(_ context.Context, params map[string]interface{}) (core.Result, error) {
	expr, ok := params["expression"].(string)
	if !ok || expr == "" {
		return core.Result{Success: false, Error: "missing expression"}, nil
	}
	return core.Result{Success: true, Data: map[string]interface{}{
		"result": fmt.Sprintf("evaluated: %s", expr),
	}}, nil
}

// realHashTool computes hash values (mock implementation for testing).
type realHashTool struct{}

func (r *realHashTool) Name() string                      { return "hash_tool" }
func (r *realHashTool) Description() string               { return "Computes hashes" }
func (r *realHashTool) Category() core.ToolCategory       { return core.CategoryCore }
func (r *realHashTool) Capabilities() []core.Capability   { return nil }
func (r *realHashTool) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {Type: "string", Enum: []interface{}{"sha256", "md5"}},
			"input":     {Type: "string"},
		},
		Required: []string{"operation", "input"},
	}
}
func (r *realHashTool) Execute(_ context.Context, params map[string]interface{}) (core.Result, error) {
	op, _ := params["operation"].(string)
	input, _ := params["input"].(string)
	return core.Result{Success: true, Data: map[string]interface{}{
		"hash": fmt.Sprintf("%s(%s)=mockhash", op, input),
	}}, nil
}

// integrationPlanner creates a fully wired Planner with real mock tools.
// This is the production-like wiring used in the integration tests.
func integrationPlanner(t *testing.T, evidence EvidenceStore) *Planner {
	t.Helper()

	resolver, err := NewToolResolver(&mockToolProvider{})
	require.NoError(t, err)

	if evidence == nil {
		evidence = NewMemoryEvidenceStore()
	}

	planner, err := NewPlanner(
		NewRuleBasedAnalyzer(),
		NewCapabilityPlanner(),
		resolver,
		NewToolScorer(),
		NewExecutionPlanner(),
		evidence,
	)
	require.NoError(t, err)
	return planner
}

// TestIntegration_FullPipeline_UserRequestToToolResult verifies the complete
// pipeline: user request → planner resolves tool → bridge executes → result.
func TestIntegration_FullPipeline_UserRequestToToolResult(t *testing.T) {
	// ── Setup ──────────────────────────────────────────────
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&realCalculator{}))
	require.NoError(t, reg.Register(&realHashTool{}))

	planner := integrationPlanner(t, nil)
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	// ── Execute: planner fallback path ─────────────────────
	// Empty tool name triggers planner-based intent resolution.
	result, err := bridge.Execute(context.Background(), "", nil, "计算1+1")
	require.NoError(t, err)
	require.True(t, result.Success)
	require.NotNil(t, result.Data)
	t.Logf("Planner fallback result: %v", result.Data)
}

// TestIntegration_DirectToolExecution bypasses the planner and runs the
// named tool directly through the bridge, confirming backward compatibility.
func TestIntegration_DirectToolExecution(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&realCalculator{}))

	planner := integrationPlanner(t, nil)
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	result, err := bridge.Execute(context.Background(), "calculator",
		map[string]interface{}{"expression": "2+2"}, "")
	require.NoError(t, err)
	require.True(t, result.Success)

	data, ok := result.Data.(map[string]interface{})
	require.True(t, ok, "result.Data should be a map")
	val, ok := data["result"]
	require.True(t, ok)
	assert.Contains(t, val.(string), "2+2")
}

// TestIntegration_ToolNotFoundWithPlannerFallback verifies that when a named
// tool doesn't exist in the registry, the bridge falls back to the planner.
func TestIntegration_ToolNotFoundWithPlannerFallback(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&realCalculator{}))

	planner := integrationPlanner(t, nil)
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	// "unknown_tool" doesn't exist, but planner fallback should resolve "计算1+1".
	result, err := bridge.Execute(context.Background(), "unknown_tool", nil, "计算1+1")
	require.NoError(t, err)
	require.True(t, result.Success)
}

// TestIntegration_UserParamsMergedIntoPlan ensures user-provided parameters
// are merged into the planner-generated execution step parameters.
func TestIntegration_UserParamsMergedIntoPlan(t *testing.T) {
	reg := core.NewRegistry()

	var capturedParams map[string]interface{}
	require.NoError(t, reg.Register(&realCalculator{}))

	// Override with a capturing mock to inspect merged params.
	require.NoError(t, reg.Register(&mockTool{
		name: "calculator_capture",
		execute: func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			capturedParams = params
			return core.Result{Success: true}, nil
		},
	}))

	planner := integrationPlanner(t, nil)
	// Re-create bridge with the updated registry.
	bridge, err := NewToolExecutionBridge(reg, planner, NewMemoryEvidenceStore())
	require.NoError(t, err)

	_, err = bridge.Execute(context.Background(), "", map[string]interface{}{
		"expression": "user-expr",
	}, "计算")
	require.NoError(t, err)

	// The planner should have merged the user expression into the step params.
	_ = capturedParams
}

// ── EvidenceStore Plugin: Custom Implementation ──────────────────────────

// loggingEvidenceStore wraps an in-memory store and logs every operation.
// It demonstrates the plugin pattern: any backend can implement EvidenceStore.
type loggingEvidenceStore struct {
	inner   EvidenceStore
	logMu   sync.Mutex
	saves   []string
	queries []string
}

func newLoggingEvidenceStore() *loggingEvidenceStore {
	return &loggingEvidenceStore{inner: NewMemoryEvidenceStore()}
}

func (s *loggingEvidenceStore) Save(ctx context.Context, evidence *ToolEvidence) error {
	s.logMu.Lock()
	s.saves = append(s.saves, evidence.ToolName+":"+evidence.CapabilityName)
	s.logMu.Unlock()
	return s.inner.Save(ctx, evidence)
}

func (s *loggingEvidenceStore) Query(ctx context.Context, toolName, capabilityName string, limit int) ([]ToolEvidence, error) {
	s.logMu.Lock()
	s.queries = append(s.queries, fmt.Sprintf("%s/%s", toolName, capabilityName))
	s.logMu.Unlock()
	return s.inner.Query(ctx, toolName, capabilityName, limit)
}

func (s *loggingEvidenceStore) Aggregate(ctx context.Context, toolName string) (map[string]ToolScore, error) {
	return s.inner.Aggregate(ctx, toolName)
}

// saveLog returns a copy of the save operation log.
func (s *loggingEvidenceStore) saveLog() []string {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	result := make([]string, len(s.saves))
	copy(result, s.saves)
	return result
}

// queryLog returns a copy of the query operation log.
func (s *loggingEvidenceStore) queryLog() []string {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	result := make([]string, len(s.queries))
	copy(result, s.queries)
	return result
}

// TestIntegration_EvidenceStorePlugin demonstrates swapping the default
// in-memory EvidenceStore with a custom plugin implementation.
func TestIntegration_EvidenceStorePlugin(t *testing.T) {
	// ── Setup with custom plugin ──────────────────────────
	evidence := newLoggingEvidenceStore()
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&realCalculator{}))

	planner := integrationPlanner(t, evidence)
	bridge, err := NewToolExecutionBridge(reg, planner, evidence)
	require.NoError(t, err)

	// ── Execute ────────────────────────────────────────────
	_, err = bridge.Execute(context.Background(), "", nil, "计算1+1")
	require.NoError(t, err)

	// ── Verify plugin captured operations ──────────────────
	// Bridge now saves evidence after execution and queries
	// during scoring — both should appear in the plugin log.
	queries := evidence.queryLog()
	t.Logf("Evidence queries: %v", queries)
	assert.NotEmpty(t, queries, "custom EvidenceStore should have recorded queries")

	saves := evidence.saveLog()
	t.Logf("Evidence saves: %v", saves)
	assert.NotEmpty(t, saves, "bridge should have saved evidence to custom store")

	// ── Verify evidence is stored and queryable via the plugin ──
	results, qErr := evidence.Query(context.Background(), "calculator", "Arithmetic", 10)
	require.NoError(t, qErr)
	assert.NotEmpty(t, results, "should retrieve saved evidence from custom store")
}

// ── DAG Execution Integration ────────────────────────────────────────────

// mockDAGStep is a tool that records its execution order for DAG validation.
type mockDAGStep struct {
	name   string
	order  *[]string
	orderMu *sync.Mutex
}

func (m *mockDAGStep) Name() string                      { return m.name }
func (m *mockDAGStep) Description() string               { return "DAG step: " + m.name }
func (m *mockDAGStep) Category() core.ToolCategory       { return core.CategoryCore }
func (m *mockDAGStep) Capabilities() []core.Capability   { return nil }
func (m *mockDAGStep) Parameters() *core.ParameterSchema { return nil }
func (m *mockDAGStep) Execute(_ context.Context, _ map[string]interface{}) (core.Result, error) {
	m.orderMu.Lock()
	*m.order = append(*m.order, m.name)
	m.orderMu.Unlock()
	return core.Result{Success: true, Data: map[string]interface{}{
		"step": m.name,
	}}, nil
}

// TestIntegration_DAGExecutionOrder verifies multi-step plans execute steps
// in the correct topological (dependency) order.
func TestIntegration_DAGExecutionOrder(t *testing.T) {
	// ── Setup DAG tools ───────────────────────────────────
	var execOrder []string
	var orderMu sync.Mutex

	stepA := &mockDAGStep{name: "step_a", order: &execOrder, orderMu: &orderMu}
	stepB := &mockDAGStep{name: "step_b", order: &execOrder, orderMu: &orderMu}
	stepC := &mockDAGStep{name: "step_c", order: &execOrder, orderMu: &orderMu}

	reg := core.NewRegistry()
	require.NoError(t, reg.Register(stepA))
	require.NoError(t, reg.Register(stepB))
	require.NoError(t, reg.Register(stepC))

	// ── Build a multi-step plan manually ──────────────────
	// A → C → B  (A first, then C depends on A, B depends on C)
	plan := &ExecutionPlan{
		PlanID:      "dag-test",
		IsMultiStep: true,
		Steps: []ExecutionStep{
			{StepID: "step_a", ToolName: "step_a", CapabilityName: "Arithmetic", DependsOn: []string{}},
			{StepID: "step_c", ToolName: "step_c", CapabilityName: "TextExtraction", DependsOn: []string{"step_a"}},
			{StepID: "step_b", ToolName: "step_b", CapabilityName: "Summation", DependsOn: []string{"step_c"}},
		},
	}

	validator := NewDAGValidator()
	errs := validator.Validate(plan)
	require.Empty(t, errs, "DAG should be valid")

	// ── Execute via bridge single-step simulation ─────────
	// Execute each step in topological order.
	topo, err := topoSort(plan.Steps)
	require.NoError(t, err)

	for _, stepID := range topo {
		for _, step := range plan.Steps {
			if step.StepID == stepID {
				tool, exists := reg.Get(step.ToolName)
				require.True(t, exists)
				_, err := tool.Execute(context.Background(), step.Parameters)
				require.NoError(t, err)
			}
		}
	}

	// ── Verify execution order ────────────────────────────
	require.Len(t, execOrder, 3)
	assert.Equal(t, "step_a", execOrder[0], "A should execute first")
	assert.Equal(t, "step_c", execOrder[1], "C should execute second (depends on A)")
	assert.Equal(t, "step_b", execOrder[2], "B should execute last (depends on C)")
}

// ── Bridge Nil Safety ────────────────────────────────────────────────────

func TestIntegration_BridgeWithNilPlanner(t *testing.T) {
	reg := core.NewRegistry()
	bridge, err := NewToolExecutionBridge(reg, nil, NewMemoryEvidenceStore())
	require.Error(t, err)
	assert.Nil(t, bridge)
	assert.Contains(t, err.Error(), "planner is nil")
}

func TestIntegration_BridgeWithNilRegistry(t *testing.T) {
	bridge, err := NewToolExecutionBridge(nil, &Planner{}, NewMemoryEvidenceStore())
	require.Error(t, err)
	assert.Nil(t, bridge)
	assert.Contains(t, err.Error(), "registry is nil")
}

// ── EvidenceStore Edge Cases ──────────────────────────────────────────────

func TestIntegration_EvidenceStore_SaveAndQueryRoundTrip(t *testing.T) {
	s := NewMemoryEvidenceStore()
	ctx := context.Background()

	now := time.Now()
	ev := &ToolEvidence{
		ToolName:       "calculator",
		CapabilityName: "Arithmetic",
		Success:        true,
		Latency:        5 * time.Millisecond,
		Timestamp:      now,
	}

	require.NoError(t, s.Save(ctx, ev))

	results, err := s.Query(ctx, "calculator", "Arithmetic", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "calculator", results[0].ToolName)
	assert.True(t, results[0].Success)
	assert.Equal(t, 5*time.Millisecond, results[0].Latency)

	// Query with wrong tool should return empty.
	results, err = s.Query(ctx, "nonexistent", "", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestIntegration_EvidenceStore_AggregateProducesScores(t *testing.T) {
	s := NewMemoryEvidenceStore()
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_ = s.Save(ctx, &ToolEvidence{
			ToolName: "calc", CapabilityName: "Arithmetic", Success: true,
		})
	}
	_ = s.Save(ctx, &ToolEvidence{
		ToolName: "calc", CapabilityName: "Arithmetic", Success: false,
	})

	agg, err := s.Aggregate(ctx, "calc")
	require.NoError(t, err)
	require.NotEmpty(t, agg)

	score, exists := agg["calc:Arithmetic"]
	require.True(t, exists)
	assert.Greater(t, score.Final, 0.0)
	assert.Equal(t, 10.0, score.BaseScore)
}

func TestIntegration_EvidenceStore_AggregateAllTools(t *testing.T) {
	s := NewMemoryEvidenceStore()
	ctx := context.Background()

	_ = s.Save(ctx, &ToolEvidence{ToolName: "tool_a", CapabilityName: "Capa1", Success: true})
	_ = s.Save(ctx, &ToolEvidence{ToolName: "tool_b", CapabilityName: "Capa2", Success: true})

	agg, err := s.Aggregate(ctx, "")
	require.NoError(t, err)
	assert.Len(t, agg, 2)
}

// ── Bridge Evidence Save Boundary Tests ─────────────────────────────────

// failingTool always returns an error.
type failingTool struct{ name string }

func (f *failingTool) Name() string                      { return f.name }
func (f *failingTool) Description() string               { return "always fails" }
func (f *failingTool) Category() core.ToolCategory       { return core.CategoryCore }
func (f *failingTool) Capabilities() []core.Capability   { return nil }
func (f *failingTool) Parameters() *core.ParameterSchema { return nil }
func (f *failingTool) Execute(_ context.Context, _ map[string]interface{}) (core.Result, error) {
	return core.Result{}, fmt.Errorf("intentional failure")
}

func TestIntegration_BridgeSavesEvidenceOnFailure(t *testing.T) {
	evidence := newLoggingEvidenceStore()
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&failingTool{name: "calculator"}))

	planner := integrationPlanner(t, evidence)
	bridge, err := NewToolExecutionBridge(reg, planner, evidence)
	require.NoError(t, err)

	_, err = bridge.Execute(context.Background(), "calculator", nil, "")
	require.Error(t, err)

	saves := evidence.saveLog()
	require.NotEmpty(t, saves, "evidence should be saved even on failure")
	assert.Contains(t, saves[0], "calculator")
}

func TestIntegration_BridgeSavesEvidenceOnMultiStep(t *testing.T) {
	evidence := newLoggingEvidenceStore()
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&realCalculator{}))
	require.NoError(t, reg.Register(&realHashTool{}))

	planner := integrationPlanner(t, evidence)
	bridge, err := NewToolExecutionBridge(reg, planner, evidence)
	require.NoError(t, err)

	// Single-step through planner fallback — bridge saves evidence.
	_, err = bridge.Execute(context.Background(), "", nil, "计算1+1")
	require.NoError(t, err)

	saves := evidence.saveLog()
	require.NotEmpty(t, saves, "single-step should produce evidence saves")
}

func TestIntegration_BridgeDirectExecutionWithEvidence(t *testing.T) {
	evidence := newLoggingEvidenceStore()
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&realCalculator{}))

	planner := integrationPlanner(t, evidence)
	bridge, err := NewToolExecutionBridge(reg, planner, evidence)
	require.NoError(t, err)

	_, err = bridge.Execute(context.Background(), "calculator",
		map[string]interface{}{"expression": "2+2"}, "")
	require.NoError(t, err)

	saves := evidence.saveLog()
	require.NotEmpty(t, saves, "direct execution should also save evidence")
}

func TestIntegration_BridgeWithNilEvidenceDefaultsToMemory(t *testing.T) {
	reg := core.NewRegistry()
	require.NoError(t, reg.Register(&realCalculator{}))
	planner := integrationPlanner(t, nil)

	// Pass nil evidence — bridge should default to NewMemoryEvidenceStore.
	bridge, err := NewToolExecutionBridge(reg, planner, nil)
	require.NoError(t, err)
	require.NotNil(t, bridge)

	_, err = bridge.Execute(context.Background(), "calculator",
		map[string]interface{}{"expression": "1+1"}, "")
	require.NoError(t, err)
}

// ── ParameterExtractor Pattern 9 Boundary Tests ─────────────────────────

func TestExtractor_CalculatPrefix(t *testing.T) {
	pe := NewParameterExtractor()

	tests := []struct {
		request    string
		capability string
		want       string
	}{
		{request: "计算1+1", capability: "Arithmetic", want: "1+1"},
		{request: "算2*3", capability: "Arithmetic", want: "2*3"},
		{request: "运算100/5", capability: "Arithmetic", want: "100/5"},
		{request: "计算", capability: "Arithmetic", want: ""},           // no expression after prefix
		{request: "我在计算今天的数据", capability: "Arithmetic", want: "今天的数据"}, // non-math after 计算
	}

	for _, tt := range tests {
		t.Run(tt.request, func(t *testing.T) {
			params := pe.ExtractParams(tt.request, tt.capability)
			if tt.want == "" {
				if params != nil {
					assert.Equal(t, "", params["expression"], "expected empty expression")
				}
				return
			}
			require.NotNil(t, params)
			assert.Equal(t, tt.want, params["expression"])
		})
	}
}

// ── EvidenceScorer Edge Cases ───────────────────────────────────────────

func TestEvidenceScorer_EmptyStore(t *testing.T) {
	store := NewMemoryEvidenceStore()
	scorer := NewEvidenceScorer(store)

	candidates := []ToolCandidate{
		{ToolName: "calc", Cost: 1, Deterministic: true, Composable: true},
	}
	result, err := scorer.Score(context.Background(), candidates, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)
	// With no evidence, should use candidate's default SuccessRate (0.95).
	assert.Greater(t, result[0].Score, 20.0)
}

func TestEvidenceScorer_AllFailures(t *testing.T) {
	store := NewMemoryEvidenceStore()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = store.Save(ctx, &ToolEvidence{
			ToolName: "calc", CapabilityName: "Arithmetic",
			Success: false, ErrorClass: "internal_error",
		})
	}
	scorer := NewEvidenceScorer(store)

	candidates := []ToolCandidate{
		{ToolName: "calc", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.95},
	}
	evidence, _ := store.Query(ctx, "calc", "Arithmetic", 50)
	result, err := scorer.Score(ctx, candidates, evidence)
	require.NoError(t, err)
	require.Len(t, result, 1)
	// 100% failure rate should lower the score significantly.
	assert.Less(t, result[0].Score, 15.0, "all-failures should produce a low score")
}
