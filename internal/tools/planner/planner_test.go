package planner

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Intent ────────────────────────────────────────────────

func TestIntent_Fields(t *testing.T) {
	intent := &Intent{
		Goal:                 "mathematical computation",
		Operation:            "summation",
		Complexity:           "simple",
		RequiredCapabilities: []string{"Summation", "Arithmetic"},
	}
	assert.Equal(t, "mathematical computation", intent.Goal)
	assert.Equal(t, "summation", intent.Operation)
	assert.Equal(t, "simple", intent.Complexity)
	assert.Len(t, intent.RequiredCapabilities, 2)
}

// ── SemanticAnalyzer ──────────────────────────────────────

func TestRuleBasedAnalyzer_EmptyRequest(t *testing.T) {
	a := NewRuleBasedAnalyzer()
	intent, err := a.Analyze(context.Background(), "")
	require.Error(t, err)
	assert.Nil(t, intent)
	assert.Contains(t, err.Error(), "empty request")
}

func TestRuleBasedAnalyzer_Summation(t *testing.T) {
	a := NewRuleBasedAnalyzer()
	// "计算1到一百万的和" contains "计算" which matches the arithmetic rule.
	// The arithmetic rule still allows calculator to perform the sum.
	intent, err := a.Analyze(context.Background(), "计算1到一百万的和")
	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.Equal(t, "mathematical computation", intent.Goal)
	assert.Contains(t, intent.RequiredCapabilities, "Arithmetic")
}

func TestRuleBasedAnalyzer_SummationChineseExact(t *testing.T) {
	a := NewRuleBasedAnalyzer()
	// "累加" explicitly triggers the summation rule.
	intent, err := a.Analyze(context.Background(), "从1累加到100万")
	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.Equal(t, "summation", intent.Operation)
	assert.Contains(t, intent.RequiredCapabilities, "Summation")
}

func TestRuleBasedAnalyzer_SummationEnglish(t *testing.T) {
	a := NewRuleBasedAnalyzer()
	intent, err := a.Analyze(context.Background(), "sum from 1 to 100")
	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.Equal(t, "summation", intent.Operation)
}

func TestRuleBasedAnalyzer_PDF(t *testing.T) {
	a := NewRuleBasedAnalyzer()
	intent, err := a.Analyze(context.Background(), "extract text from this pdf")
	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.Equal(t, "document processing", intent.Goal)
	assert.Contains(t, intent.RequiredCapabilities, "PDFParsing")
}

func TestRuleBasedAnalyzer_Hash(t *testing.T) {
	a := NewRuleBasedAnalyzer()
	intent, err := a.Analyze(context.Background(), "compute sha256 hash of hello")
	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.Equal(t, "cryptographic operation", intent.Goal)
	assert.Contains(t, intent.RequiredCapabilities, "Hashing")
}

func TestRuleBasedAnalyzer_UnknownRequest(t *testing.T) {
	a := NewRuleBasedAnalyzer()
	intent, err := a.Analyze(context.Background(), "some completely unknown request type")
	require.Error(t, err)
	assert.Nil(t, intent)
}

// ── CapabilityPlanner ─────────────────────────────────────

func TestCapabilityPlanner_NilIntent(t *testing.T) {
	p := NewCapabilityPlanner()
	reqs, err := p.Plan(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, reqs)
}

func TestCapabilityPlanner_EmptyCapabilities(t *testing.T) {
	p := NewCapabilityPlanner()
	reqs, err := p.Plan(context.Background(), &Intent{RequiredCapabilities: []string{}})
	require.Error(t, err)
	assert.Nil(t, reqs)
}

func TestCapabilityPlanner_SingleCapability(t *testing.T) {
	p := NewCapabilityPlanner()
	reqs, err := p.Plan(context.Background(), &Intent{
		RequiredCapabilities: []string{"Arithmetic"},
	})
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	assert.Equal(t, "Arithmetic", reqs[0].Name)
	assert.Equal(t, "Expression", reqs[0].InputType)
	assert.Equal(t, "Number", reqs[0].OutputType)
}

func TestCapabilityPlanner_Deduplicates(t *testing.T) {
	p := NewCapabilityPlanner()
	reqs, err := p.Plan(context.Background(), &Intent{
		RequiredCapabilities: []string{"Arithmetic", "Arithmetic", "Summation"},
	})
	require.NoError(t, err)
	// Duplicates should be removed, so we get 2 requirements.
	assert.Len(t, reqs, 2)
}

// ── ToolResolver ──────────────────────────────────────────

type mockToolProvider struct{}

func (m *mockToolProvider) ListTools() []string {
	return []string{
		"calculator", "hash_tool", "pdf_tool", "string_utils",
		"regex_tool", "json_tools", "web_search", "http_request",
		"id_generator", "code_runner", "embedding", "datetime",
		"data_transform", "log_analyzer", "text_processor", "task_planner",
		"web_scraper", "data_validation", "knowledge_search", "memory_search",
	}
}

func (m *mockToolProvider) GetToolCapabilities(name string) ([]string, error) {
	return nil, nil
}

func TestToolResolver_NilRequirement(t *testing.T) {
	r, err := NewToolResolver(&mockToolProvider{})
	require.NoError(t, err)
	cands, err := r.Resolve(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, cands)
}

func TestToolResolver_EmptyName(t *testing.T) {
	r, err := NewToolResolver(&mockToolProvider{})
	require.NoError(t, err)
	cands, err := r.Resolve(context.Background(), &CapabilityRequirement{Name: ""})
	require.Error(t, err)
	assert.Nil(t, cands)
}

func TestToolResolver_KnownCapability(t *testing.T) {
	r, err := NewToolResolver(&mockToolProvider{})
	require.NoError(t, err)
	cands, err := r.Resolve(context.Background(), &CapabilityRequirement{Name: "Arithmetic"})
	require.NoError(t, err)
	require.Len(t, cands, 1)
	assert.Equal(t, "calculator", cands[0].ToolName)
	assert.True(t, cands[0].Deterministic)
}

func TestToolResolver_UnknownCapability(t *testing.T) {
	r, err := NewToolResolver(&mockToolProvider{})
	require.NoError(t, err)
	cands, err := r.Resolve(context.Background(), &CapabilityRequirement{Name: "UnknownCapa"})
	require.Error(t, err)
	assert.Nil(t, cands)
}

func TestToolResolver_MultipleCandidates(t *testing.T) {
	r, err := NewToolResolver(&mockToolProvider{})
	require.NoError(t, err)
	cands, err := r.Resolve(context.Background(), &CapabilityRequirement{Name: "WebFetch"})
	require.NoError(t, err)
	require.Len(t, cands, 2)
	// Should have both web_search and http_request.
	names := []string{cands[0].ToolName, cands[1].ToolName}
	assert.Contains(t, names, "web_search")
	assert.Contains(t, names, "http_request")
}

// ── ToolScorer ────────────────────────────────────────────

func TestToolScorer_EmptyCandidates(t *testing.T) {
	s := NewToolScorer()
	result, err := s.Score(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestToolScorer_SingleCandidate(t *testing.T) {
	s := NewToolScorer()
	candidates := []ToolCandidate{
		{ToolName: "calculator", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.95},
	}
	result, err := s.Score(context.Background(), candidates, nil)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "calculator", result[0].ToolName)
	// Deterministic + composable + low cost should give a high score.
	assert.Greater(t, result[0].Score, 20.0)
}

func TestToolScorer_Ranking(t *testing.T) {
	s := NewToolScorer()
	candidates := []ToolCandidate{
		{ToolName: "web_search", Cost: 5, Deterministic: false, Composable: true,
			SideEffects: false, SuccessRate: 0.80},
		{ToolName: "http_request", Cost: 5, Deterministic: false, Composable: true,
			SideEffects: true, SuccessRate: 0.90},
	}
	result, err := s.Score(context.Background(), candidates, nil)
	require.NoError(t, err)
	require.Len(t, result, 2)
	// web_search has no side-effect penalty, should rank higher.
	assert.Equal(t, "web_search", result[0].ToolName)
	assert.Equal(t, "http_request", result[1].ToolName)
}

func TestToolScorer_EvidenceBoostsScore(t *testing.T) {
	s := NewToolScorer()
	candidates := []ToolCandidate{
		{ToolName: "calculator", Cost: 1, Deterministic: true, Composable: true, SuccessRate: 0.50},
	}
	evidence := []ToolEvidence{
		{ToolName: "calculator", Success: true, Latency: 1 * time.Millisecond},
		{ToolName: "calculator", Success: true, Latency: 2 * time.Millisecond},
		{ToolName: "calculator", Success: true, Latency: 1 * time.Millisecond},
	}
	result, err := s.Score(context.Background(), candidates, evidence)
	require.NoError(t, err)
	require.Len(t, result, 1)
	// With 100% success evidence, the score should be higher than default 0.95.
	assert.Greater(t, result[0].Score, 25.0)
}

func TestToolScorer_SideEffectPenalty(t *testing.T) {
	s := NewToolScorer()
	candidates := []ToolCandidate{
		{ToolName: "no-side-effect", Cost: 1, Deterministic: true, Composable: true,
			SideEffects: false, SuccessRate: 0.95},
		{ToolName: "has-side-effect", Cost: 1, Deterministic: true, Composable: true,
			SideEffects: true, SuccessRate: 0.95},
	}
	result, err := s.Score(context.Background(), candidates, nil)
	require.NoError(t, err)
	require.Len(t, result, 2)
	// Non-side-effect tool should rank higher due to no penalty.
	assert.Equal(t, "no-side-effect", result[0].ToolName)
	assert.Greater(t, result[0].Score, result[1].Score)
}

// ── ExecutionPlanner ──────────────────────────────────────

func TestExecutionPlanner_NilIntent(t *testing.T) {
	p := NewExecutionPlanner()
	plan, err := p.Plan(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Nil(t, plan)
}

func TestExecutionPlanner_NoRequirements(t *testing.T) {
	p := NewExecutionPlanner()
	plan, err := p.Plan(context.Background(), &Intent{Goal: "test"}, nil)
	require.Error(t, err)
	assert.Nil(t, plan)
}

func TestExecutionPlanner_SingleStep(t *testing.T) {
	p := NewExecutionPlanner()
	plan, err := p.Plan(context.Background(), &Intent{Goal: "summation", RequiredCapabilities: []string{"Summation"}},
		[]CapabilityRequirement{
			{Name: "Summation", InputType: "Expression", OutputType: "Number"},
		})
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.False(t, plan.IsMultiStep)
	require.Len(t, plan.Steps, 1)
	assert.Equal(t, "Summation", plan.Steps[0].CapabilityName)
	assert.NotEmpty(t, plan.PlanID)
}

func TestExecutionPlanner_MultiStep(t *testing.T) {
	p := NewExecutionPlanner()
	plan, err := p.Plan(context.Background(),
		&Intent{Goal: "document processing", RequiredCapabilities: []string{"PDFParsing", "TextExtraction"}},
		[]CapabilityRequirement{
			{Name: "PDFParsing", InputType: "File", OutputType: "Text"},
			{Name: "TextExtraction", InputType: "Text", OutputType: "Text", DependsOn: []string{"PDFParsing"}},
		})
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.True(t, plan.IsMultiStep)
	require.Len(t, plan.Steps, 2)
}

// ── EvidenceStore ─────────────────────────────────────────

func TestMemoryEvidenceStore_SaveNil(t *testing.T) {
	s := NewMemoryEvidenceStore()
	err := s.Save(context.Background(), nil)
	require.Error(t, err)
}

func TestMemoryEvidenceStore_SaveAndQuery(t *testing.T) {
	s := NewMemoryEvidenceStore()
	ctx := context.Background()

	err := s.Save(ctx, &ToolEvidence{
		ToolName: "calculator", CapabilityName: "Arithmetic",
		Success: true, Latency: 2 * time.Millisecond,
	})
	require.NoError(t, err)

	results, err := s.Query(ctx, "calculator", "", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Success)
}

func TestMemoryEvidenceStore_QueryByCapability(t *testing.T) {
	s := NewMemoryEvidenceStore()
	ctx := context.Background()

	_ = s.Save(ctx, &ToolEvidence{ToolName: "calculator", CapabilityName: "Arithmetic"})
	_ = s.Save(ctx, &ToolEvidence{ToolName: "hash_tool", CapabilityName: "Hashing"})

	results, err := s.Query(ctx, "", "Arithmetic", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "calculator", results[0].ToolName)
}

func TestMemoryEvidenceStore_QueryEmpty(t *testing.T) {
	s := NewMemoryEvidenceStore()
	results, err := s.Query(context.Background(), "", "", 10)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestMemoryEvidenceStore_Aggregate(t *testing.T) {
	s := NewMemoryEvidenceStore()
	ctx := context.Background()

	_ = s.Save(ctx, &ToolEvidence{ToolName: "calc", CapabilityName: "Arithmetic", Success: true})
	_ = s.Save(ctx, &ToolEvidence{ToolName: "calc", CapabilityName: "Arithmetic", Success: false})

	agg, err := s.Aggregate(ctx, "calc")
	require.NoError(t, err)
	assert.NotNil(t, agg)
}

// ── Planner Integration ──────────────────────────────────

func testPlanner(t *testing.T) *Planner {
	t.Helper()
	resolver, err := NewToolResolver(&mockToolProvider{})
	require.NoError(t, err)
	store := NewMemoryEvidenceStore()
	planner, err := NewPlanner(
		NewRuleBasedAnalyzer(),
		NewCapabilityPlanner(),
		resolver,
		NewEvidenceScorer(store),
		NewExecutionPlanner(),
		store,
	)
	require.NoError(t, err)
	return planner
}

func TestPlanner_FullPipeline_Summation(t *testing.T) {
	planner := testPlanner(t)

	plan, err := planner.Plan(context.Background(), "计算1到一百万的和")
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, "mathematical computation", plan.Intent.Goal)
	assert.NotEmpty(t, plan.Steps)
	assert.NotEmpty(t, plan.Steps[0].ToolName)
}

func TestPlanner_EmptyRequest(t *testing.T) {
	planner := testPlanner(t)

	plan, err := planner.Plan(context.Background(), "")
	require.Error(t, err)
	assert.Nil(t, plan)
}

func TestPlanner_UnknownRequest(t *testing.T) {
	planner := testPlanner(t)

	plan, err := planner.Plan(context.Background(), "do something completely unknown")
	require.Error(t, err)
	assert.Nil(t, plan)
}

func TestPlanner_PDFRequest(t *testing.T) {
	planner := testPlanner(t)

	plan, err := planner.Plan(context.Background(), "extract text from this pdf")
	require.NoError(t, err)
	require.NotNil(t, plan)
	assert.Equal(t, "document processing", plan.Intent.Goal)
}
