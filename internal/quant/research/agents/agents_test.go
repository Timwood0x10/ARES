package agents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"goagentx/internal/quant/research"
)

// ─── Prompt Builder Tests ─────────────────────────────────

func TestBuildMarketAnalystPrompt_ContainsSymbol(t *testing.T) {
	state := &research.ResearchState{Symbol: "AAPL"}
	prompt := BuildMarketAnalystPrompt(state)
	if !contains(prompt, "AAPL") {
		t.Error("prompt should contain symbol AAPL")
	}
}

func TestBuildMarketAnalystPrompt_InjectsSnapshot(t *testing.T) {
	now := time.Now()
	snapshot := &research.VerifiedMarketSnapshot{
		RequestedDate: now,
		LatestRowDate: now,
		OHLCV: &research.Candle{
			Ticker: "AAPL",
			Date:   now,
			Open:   150.0,
			High:   155.0,
			Low:    149.0,
			Close:  153.0,
			Volume: 1000000,
		},
		Indicators:   map[string]float64{"RSI": 65.5, "MACD": 2.3},
		RecentCloses: []float64{150.0, 151.0, 152.0, 153.0},
	}
	state := &research.ResearchState{Symbol: "AAPL", MarketSnapshot: snapshot}
	prompt := BuildMarketAnalystPrompt(state)
	if !contains(prompt, "Verified Market Snapshot") {
		t.Error("prompt should contain snapshot section when snapshot is provided")
	}
	if !contains(prompt, "RSI") {
		t.Error("prompt should contain RSI indicator")
	}
}

func TestBuildSentimentAnalystPrompt_HasBandAndScore(t *testing.T) {
	state := &research.ResearchState{Symbol: "TSLA"}
	prompt := BuildSentimentAnalystPrompt(state)
	if !contains(prompt, "band") || !contains(prompt, "score") {
		t.Error("sentiment prompt should require band and score output")
	}
	if !contains(prompt, "confidence") {
		t.Error("sentiment prompt should require confidence output")
	}
}

func TestBuildTraderPrompt_ContainsResearchPlan(t *testing.T) {
	state := &research.ResearchState{
		Symbol: "MSFT",
		ResearchPlan: &research.ResearchPlan{
			Recommendation:  research.RatingBuy,
			Rationale:       "Strong cloud growth",
			StrategicAction: "Accumulate on dips",
		},
	}
	prompt := BuildTraderPrompt(state)
	if !contains(prompt, "Research Plan") {
		t.Error("trader prompt should contain research plan section")
	}
	if !contains(prompt, "Buy") {
		t.Error("trader prompt should contain recommendation")
	}
}

func TestBuildPortfolioManagerPrompt_ContainsRiskDebate(t *testing.T) {
	state := &research.ResearchState{
		Symbol: "GOOGL",
		RiskDebateState: &research.RiskDebateState{
			Round:            1,
			MaxRounds:        2,
			AggressiveView:   "High tail risk from regulation",
			ConservativeView: "Acceptable risk-reward",
			NeutralView:      "Balanced view with caveats",
		},
	}
	prompt := BuildPortfolioManagerPrompt(state)
	if !contains(prompt, "Risk Assessment Summary") {
		t.Error("PM prompt should contain risk debate summary")
	}
	if !contains(prompt, "Aggressive View") {
		t.Error("PM prompt should contain aggressive view")
	}
}

func TestBuildBullResearcherPrompt_InjectsDebateHistory(t *testing.T) {
	state := &research.ResearchState{
		Symbol: "NVDA",
		DebateState: &research.InvestDebateState{
			Round:         2,
			BullArguments: []string{"AI demand is accelerating"},
			BearArguments: []string{"Valuation is stretched"},
		},
	}
	prompt := BuildBullResearcherPrompt(state)
	if !contains(prompt, "Previous Debate") {
		t.Error("bull researcher prompt should contain debate history")
	}
}

// ─── JSON Parser Tests ────────────────────────────────────

func TestJSONParser_PureJSON(t *testing.T) {
	parser := NewJSONParser[map[string]interface{}]()
	input := `{"ticker":"AAPL","score":8.5,"verdict":"bullish"}`
	result, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("parse pure json failed: %v", err)
	}
	if (*result)["ticker"] != "AAPL" {
		t.Errorf("expected ticker AAPL, got %v", (*result)["ticker"])
	}
}

func TestJSONParser_MarkdownCodeBlock(t *testing.T) {
	parser := NewJSONParser[map[string]interface{}]()
	input := "Here is my analysis:\n```json\n{\"ticker\":\"TSLA\",\"score\":7}\n```\nDone."
	result, err := parser.Parse(input)
	if err != nil {
		t.Fatalf("parse markdown code block failed: %v", err)
	}
	if (*result)["ticker"] != "TSLA" {
		t.Errorf("expected ticker TSLA, got %v", (*result)["ticker"])
	}
}

func TestJSONParser_MixedText(t *testing.T) {
	parser := NewJSONParser[map[string]interface{}]()
	mixedInput := "Some text before {\"key\":\"value\"} some text after"
	result, err := parser.Parse(mixedInput)
	if err != nil {
		t.Fatalf("parse mixed text failed: %v", err)
	}
	if (*result)["key"] != "value" {
		t.Errorf("expected key=value, got %v", (*result))
	}
}

func TestJSONParser_InvalidInput_ReturnsParseError(t *testing.T) {
	parser := NewJSONParser[map[string]interface{}]()
	_, err := parser.Parse("this is not json at all")
	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Error("should return ParseError for invalid input")
	}
	if parseErr.RawOutput() == "" {
		t.Error("ParseError should preserve raw output")
	}
}

// ─── Markdown Parser Tests ────────────────────────────────

func TestMarkdownParser_ParseRating(t *testing.T) {
	parser := NewMarkdownParser()
	tests := []struct {
		input    string
		expected research.PortfolioRating
	}{
		{"We recommend Buy", research.RatingBuy},
		{"Strong Sell signal", research.RatingSell},
		{"Overweight position", research.RatingOverweight},
		{"Hold current", research.RatingHold},
		{"Underweight due to risks", research.RatingUnderweight},
		{"No rating mentioned", research.RatingHold}, // default
	}
	for _, tt := range tests {
		t.Run(tt.input[:10], func(t *testing.T) {
			rating, _ := parser.ParseRating(tt.input)
			if rating != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, rating)
			}
		})
	}
}

func TestMarkdownParser_ParseScore(t *testing.T) {
	parser := NewMarkdownParser()
	score, err := parser.ParseScore("Overall score: 7.5 / 10")
	if err != nil {
		t.Fatalf("parse score failed: %v", err)
	}
	if score != 7.5 {
		t.Errorf("expected 7.5, got %f", score)
	}
}

func TestMarkdownParser_ParsePortfolioDecision(t *testing.T) {
	parser := NewMarkdownParser()
	md := `# Decision
## Rating: Buy
## Summary: Strong buy based on AI momentum.
## Thesis: NVDA dominates datacenter AI.
## Target: $1000`
	decision, err := parser.ParsePortfolioDecision(md)
	if err != nil {
		t.Fatalf("parse portfolio decision failed: %v", err)
	}
	if decision.Rating != research.RatingBuy {
		t.Errorf("expected Buy rating, got %s", decision.Rating)
	}
}

// ─── Mock Executor Tests ──────────────────────────────────

func TestMockLLMExecutor_SetResponseAndComplete(t *testing.T) {
	mock := NewMockLLMExecutor()
	mock.SetResponse("You are a Market", `{"ticker":"AAPL","score":8}`)
	ctx := context.Background()
	resp, err := mock.Complete(ctx, []Message{{Role: "user", Content: "You are a Market Analyst for AAPL"}})
	if err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	if resp != `{"ticker":"AAPL","score":8}` {
		t.Errorf("unexpected response: %s", resp)
	}
	if mock.CallCount() != 1 {
		t.Errorf("expected call count 1, got %d", mock.CallCount())
	}
}

func TestMockLLMExecutor_ErrorInjection(t *testing.T) {
	mock := NewMockLLMExecutor()
	testErr := errors.New("rate limited")
	mock.SetError("You are a", testErr)
	ctx := context.Background()
	_, err := mock.Complete(ctx, []Message{{Role: "user", Content: "You are a Trader"}})
	if err == nil {
		t.Fatal("expected error from injected error")
	}
	if !errors.Is(err, testErr) {
		t.Errorf("expected injected error, got: %v", err)
	}
}

func TestMockLLMExecutor_CompleteStructuredFails(t *testing.T) {
	mock := NewMockLLMExecutor()
	ctx := context.Background()
	_, err := mock.CompleteStructured(ctx, []Message{}, nil)
	if err == nil {
		t.Fatal("structured complete should fail in mock")
	}
}

func TestMockLLMExecutor_LongestPrefixWins(t *testing.T) {
	mock := NewMockLLMExecutor()
	mock.SetResponse("You are a", "generic response")
	mock.SetResponse("You are a Market", "market response")
	ctx := context.Background()
	resp, _ := mock.Complete(ctx, []Message{{Role: "user", Content: "You are a Market Analyst"}})
	if resp != "market response" {
		t.Errorf("longest prefix should win, got: %s", resp)
	}
}

func TestMockLLMExecutor_Reset(t *testing.T) {
	mock := NewMockLLMExecutor()
	mock.SetResponse("test", "response")
	mock.Reset()
	if mock.CallCount() != 0 {
		t.Error("reset should clear call count")
	}
}

// ─── BaseAgent Execute Tests ──────────────────────────────

type stateCaptureParser struct {
	lastRaw string
}

func (p *stateCaptureParser) Parse(raw string, target interface{}) error {
	p.lastRaw = raw
	return nil
}

func TestBaseAgent_Execute_FullPipeline(t *testing.T) {
	mock := NewMockLLMExecutor()
	mock.SetResponse("You are a", `{"action":"buy","reasoning":"strong momentum"}`)
	parser := &stateCaptureParser{}
	agent := NewBaseAgent("Test Trader", " trader", mock, parser, func(s *research.ResearchState) string {
		return "You are a Test Trader. Analyze " + s.Symbol + "."
	})

	state := &research.ResearchState{Symbol: "AAPL"}
	err := agent.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if state.CurrentStep != "Test Trader" {
		t.Errorf("current step should be Test Trader, got %s", state.CurrentStep)
	}
	if len(state.StepsCompleted) != 1 || state.StepsCompleted[0] != "Test Trader" {
		t.Errorf("steps completed should contain Test Trader, got %v", state.StepsCompleted)
	}
	if parser.lastRaw != `{"action":"buy","reasoning":"strong momentum"}` {
		t.Errorf("parser should receive raw LLM output")
	}
}

func TestBaseAgent_ExecuteStructured_Fallback(t *testing.T) {
	mock := NewMockLLMExecutor()
	// Structured always fails in mock, so it falls back to Complete
	mock.SetResponse("You are a", `{"rating":"Buy","summary":"test"}`)
	parser := &stateCaptureParser{}
	agent := NewBaseAgent("PM", "manager", mock, parser, func(s *research.ResearchState) string {
		return "You are a PM for " + s.Symbol
	})

	state := &research.ResearchState{Symbol: "MSFT"}
	err := agent.ExecuteStructured(context.Background(), state, &research.PortfolioDecision{})
	if err != nil {
		t.Fatalf("execute structured fallback failed: %v", err)
	}
	if state.CurrentStep != "PM" {
		t.Errorf("current step should be PM after fallback")
	}
}

// ─── Helper Functions ─────────────────────────────────────

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test helper: verify JSON output has required fields for sentiment analyst.
func TestSentimentOutputSchema(t *testing.T) {
	type SentimentOutput struct {
		Ticker     string   `json:"ticker"`
		Score      float64  `json:"score"`
		Band       string   `json:"band"`
		Confidence float64  `json:"confidence"`
		Signals    []string `json:"signals"`
		Reasoning  string   `json:"reasoning"`
	}
	raw := `{"ticker":"AAPL","score":0.75,"band":"bullish","confidence":0.8,"signals":["options flow bullish"],"reasoning":"strong momentum"}`
	parser := NewJSONParser[SentimentOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse sentiment output failed: %v", err)
	}
	if result.Band != "bullish" {
		t.Errorf("expected band bullish, got %s", result.Band)
	}
	if result.Score != 0.75 {
		t.Errorf("expected score 0.75, got %f", result.Score)
	}
}

// Test that ResearchPlan can be parsed from JSON.
func TestResearchPlanJSONRoundTrip(t *testing.T) {
	plan := research.ResearchPlan{
		Recommendation:  research.RatingOverweight,
		Rationale:       "Strong fundamentals with catalyst",
		StrategicAction: "Add 5% position on weakness",
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	parser := NewJSONParser[research.ResearchPlan]()
	result, err := parser.Parse(string(data))
	if err != nil {
		t.Fatalf("parse round-trip failed: %v", err)
	}
	if result.Recommendation != plan.Recommendation {
		t.Errorf("recommendation mismatch: expected %s, got %s", plan.Recommendation, result.Recommendation)
	}
}
