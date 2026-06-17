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

// ─── Schema Alignment Tests ────────────────────────────────

// analystOutput represents the JSON structure that analyst prompts ask the LLM to produce.
// It maps directly to AnalystReport fields: score(0-100), verdict, confidence, findings.
type analystOutput struct {
	Score      float64                `json:"score"`
	Verdict    string                 `json:"verdict"`
	Confidence float64                `json:"confidence"`
	Findings   map[string]interface{} `json:"findings"`
}

func (o *analystOutput) ToReport(analystName, analystType string) *research.AnalystReport {
	return &research.AnalystReport{
		AnalystName: analystName,
		AnalystType: analystType,
		Score:       o.Score,
		Verdict:     o.Verdict,
		Confidence:  o.Confidence,
		Findings:    o.Findings,
		Timestamp:   time.Now(),
	}
}

// sentimentOutput maps to SentimentReport schema.
type sentimentOutput struct {
	Band       string   `json:"band"`
	Score      float64  `json:"score"`
	Confidence float64  `json:"confidence"`
	Signals    []string `json:"signals"`
}

func (o *sentimentOutput) ToReport() *research.SentimentReport {
	return &research.SentimentReport{
		Band:       o.Band,
		Score:      o.Score,
		Confidence: o.Confidence,
		Signals:    o.Signals,
	}
}

// researchPlanOutput maps to ResearchPlan schema.
type researchPlanOutput struct {
	Recommendation  string `json:"recommendation"`
	Rationale       string `json:"rationale"`
	StrategicAction string `json:"strategic_action"`
}

func (o *researchPlanOutput) ToPlan() *research.ResearchPlan {
	return &research.ResearchPlan{
		Recommendation:  research.PortfolioRating(o.Recommendation),
		Rationale:       o.Rationale,
		StrategicAction: o.StrategicAction,
	}
}

// traderOutput maps to TraderProposal schema.
type traderOutput struct {
	Action         string   `json:"action"`
	Reasoning      string   `json:"reasoning"`
	EntryPrice     *float64 `json:"entry_price,omitempty"`
	StopLoss       *float64 `json:"stop_loss,omitempty"`
	PositionSizing string   `json:"position_sizing,omitempty"`
}

func (o *traderOutput) ToProposal() *research.TraderProposal {
	return &research.TraderProposal{
		Action:         o.Action,
		Reasoning:      o.Reasoning,
		EntryPrice:     o.EntryPrice,
		StopLoss:       o.StopLoss,
		PositionSizing: o.PositionSizing,
	}
}

// portfolioDecisionOutput maps to PortfolioDecision schema.
type portfolioDecisionOutput struct {
	Rating           string   `json:"rating"`
	ExecutiveSummary string   `json:"executive_summary"`
	InvestmentThesis string   `json:"investment_thesis"`
	PriceTarget      *float64 `json:"price_target,omitempty"`
	TimeHorizon      string   `json:"time_horizon,omitempty"`
}

func (o *portfolioDecisionOutput) ToDecision() *research.PortfolioDecision {
	return &research.PortfolioDecision{
		Rating:           research.PortfolioRating(o.Rating),
		ExecutiveSummary: o.ExecutiveSummary,
		InvestmentThesis: o.InvestmentThesis,
		PriceTarget:      o.PriceTarget,
		TimeHorizon:      o.TimeHorizon,
	}
}

// ─── Analyst Prompt Alignment: Market ─────────────────────

func TestBuildMarketAnalystPrompt_OutputMatchesSchema(t *testing.T) {
	state := &research.ResearchState{Symbol: "AAPL"}
	prompt := BuildMarketAnalystPrompt(state)

	if !contains(prompt, "score") {
		t.Error("prompt should mention score")
	}
	if !contains(prompt, "verdict") {
		t.Error("prompt should mention verdict")
	}
	if !contains(prompt, "confidence") {
		t.Error("prompt should mention confidence")
	}
	if !contains(prompt, "findings") {
		t.Error("prompt should mention findings")
	}
	if !contains(prompt, "bullish") {
		t.Error("prompt should mention bullish as verdict option")
	}
}

func TestBuildMarketAnalyst_ValidJSON_ParsesToAnalystReport(t *testing.T) {
	raw := `{"score":68,"verdict":"bullish","confidence":0.72,"findings":{"trend":"uptrend","reasoning":"Bullish setup."}}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("Market Analyst", "market")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate failed: %v", err)
	}
}

func TestBuildMarketAnalyst_MissingFindings_StillValid(t *testing.T) {
	raw := `{"score":50,"verdict":"neutral","confidence":0.5}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("Market Analyst", "market")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate should accept missing findings: %v", err)
	}
}

func TestBuildMarketAnalyst_InvalidVerdict_Rejected(t *testing.T) {
	raw := `{"score":50,"verdict":"super_bullish","confidence":0.5}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("Market Analyst", "market")
	if err := report.Validate(); err == nil {
		t.Error("AnalystReport.Validate should reject invalid verdict")
	}
}

func TestBuildMarketAnalyst_ScoreOutOfRange_Rejected(t *testing.T) {
	raw := `{"score":150,"verdict":"bullish","confidence":0.5}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("Market Analyst", "market")
	if err := report.Validate(); err == nil {
		t.Error("AnalystReport.Validate should reject score > 100")
	}
}

func TestBuildMarketAnalyst_ConfidenceOutOfRange_Rejected(t *testing.T) {
	raw := `{"score":80,"verdict":"bullish","confidence":1.5}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("Market Analyst", "market")
	if err := report.Validate(); err == nil {
		t.Error("AnalystReport.Validate should reject confidence > 1")
	}
}

func TestBuildMarketAnalyst_MarkdownWrappedJSON_ParsedByJSONParser(t *testing.T) {
	raw := "Here is my analysis:\n```json\n{\"score\":68,\"verdict\":\"bullish\",\"confidence\":0.72,\"findings\":{\"trend\":\"uptrend\"}}\n```\nDone."
	parser := NewJSONParser[analystOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse markdown-wrapped JSON failed: %v", err)
	}
	report := result.ToReport("Market Analyst", "market")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate failed: %v", err)
	}
	if result.Verdict != "bullish" {
		t.Errorf("expected verdict bullish, got %s", result.Verdict)
	}
}

// ─── Analyst Prompt Alignment: Sentiment ──────────────────

func TestBuildSentimentAnalystPrompt_OutputMatchesSchema(t *testing.T) {
	state := &research.ResearchState{Symbol: "TSLA"}
	prompt := BuildSentimentAnalystPrompt(state)

	if !contains(prompt, "band") {
		t.Error("prompt should mention band")
	}
	if !contains(prompt, "score") {
		t.Error("prompt should mention score")
	}
	if !contains(prompt, "confidence") {
		t.Error("prompt should mention confidence")
	}
	if !contains(prompt, "signals") {
		t.Error("prompt should mention signals")
	}
}

func TestBuildSentimentAnalyst_ValidJSON_ParsesToSentimentReport(t *testing.T) {
	raw := `{"band":"bullish","score":0.62,"confidence":0.72,"signals":["positive_social_sentiment"]}`
	var out sentimentOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport()
	if err := report.Validate(); err != nil {
		t.Errorf("SentimentReport.Validate failed: %v", err)
	}
}

func TestBuildSentimentAnalyst_MarkdownWrappedJSON_Parsed(t *testing.T) {
	raw := "```json\n{\"band\":\"bearish\",\"score\":0.3,\"confidence\":0.8,\"signals\":[\"weak_volume\"]}\n```"
	parser := NewJSONParser[sentimentOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse markdown-wrapped JSON failed: %v", err)
	}
	report := result.ToReport()
	if err := report.Validate(); err != nil {
		t.Errorf("SentimentReport.Validate failed: %v", err)
	}
	if result.Band != "bearish" {
		t.Errorf("expected band bearish, got %s", result.Band)
	}
}

func TestBuildSentimentAnalyst_InvalidBand_Rejected(t *testing.T) {
	raw := `{"band":"extreme_bullish","score":0.9,"confidence":0.8,"signals":[]}`
	var out sentimentOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport()
	if err := report.Validate(); err == nil {
		t.Error("SentimentReport.Validate should reject invalid band")
	}
}

func TestBuildSentimentAnalyst_MissingSignals_StillValid(t *testing.T) {
	raw := `{"band":"neutral","score":0.5,"confidence":0.5}`
	var out sentimentOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if out.Signals != nil {
		t.Error("expected nil signals for missing field")
	}
	report := out.ToReport()
	if err := report.Validate(); err != nil {
		t.Errorf("SentimentReport.Validate should accept missing signals: %v", err)
	}
}

func TestBuildSentimentAnalyst_ScoreOutOfRange_Rejected(t *testing.T) {
	raw := `{"band":"bullish","score":1.5,"confidence":0.8,"signals":[]}`
	var out sentimentOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport()
	if err := report.Validate(); err == nil {
		t.Error("SentimentReport.Validate should reject score > 1")
	}
}

// ─── Analyst Prompt Alignment: News ───────────────────────

func TestBuildNewsAnalystPrompt_OutputMatchesSchema(t *testing.T) {
	state := &research.ResearchState{Symbol: "AAPL"}
	prompt := BuildNewsAnalystPrompt(state)

	if !contains(prompt, "score") {
		t.Error("prompt should mention score")
	}
	if !contains(prompt, "verdict") {
		t.Error("prompt should mention verdict")
	}
	if !contains(prompt, "confidence") {
		t.Error("prompt should mention confidence")
	}
	if !contains(prompt, "findings") {
		t.Error("prompt should mention findings")
	}
}

func TestBuildNewsAnalyst_ValidJSON_ParsesToAnalystReport(t *testing.T) {
	raw := `{"score":65,"verdict":"bullish","confidence":0.68,"findings":{"positive_factors":["Earnings beat"],"reasoning":"Positive earnings momentum."}}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("News Analyst", "news")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate failed: %v", err)
	}
}

func TestBuildNewsAnalyst_MarkdownWrappedJSON_Parsed(t *testing.T) {
	raw := "```json\n{\"score\":30,\"verdict\":\"bearish\",\"confidence\":0.6,\"findings\":{\"negative_factors\":[\"Regulatory risk\"]}}\n```"
	parser := NewJSONParser[analystOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse markdown-wrapped JSON failed: %v", err)
	}
	report := result.ToReport("News Analyst", "news")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate failed: %v", err)
	}
}

func TestBuildNewsAnalyst_InvalidVerdict_Rejected(t *testing.T) {
	raw := `{"score":50,"verdict":"invalid_verdict","confidence":0.5}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("News Analyst", "news")
	if err := report.Validate(); err == nil {
		t.Error("AnalystReport.Validate should reject invalid verdict")
	}
}

func TestBuildNewsAnalyst_MissingFindings_StillValid(t *testing.T) {
	raw := `{"score":50,"verdict":"neutral","confidence":0.5}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("News Analyst", "news")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate should accept missing findings: %v", err)
	}
}

// ─── Analyst Prompt Alignment: Fundamentals ───────────────

func TestBuildFundamentalsAnalystPrompt_OutputMatchesSchema(t *testing.T) {
	state := &research.ResearchState{Symbol: "MSFT"}
	prompt := BuildFundamentalsAnalystPrompt(state)

	if !contains(prompt, "score") {
		t.Error("prompt should mention score")
	}
	if !contains(prompt, "verdict") {
		t.Error("prompt should mention verdict")
	}
	if !contains(prompt, "confidence") {
		t.Error("prompt should mention confidence")
	}
	if !contains(prompt, "findings") {
		t.Error("prompt should mention findings")
	}
}

func TestBuildFundamentalsAnalyst_ValidJSON_ParsesToAnalystReport(t *testing.T) {
	raw := `{"score":75,"verdict":"bullish","confidence":0.78,"findings":{"pe_ratio":"26.5","reasoning":"Strong fundamentals."}}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("Fundamentals Analyst", "fundamentals")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate failed: %v", err)
	}
}

func TestBuildFundamentalsAnalyst_MarkdownWrappedJSON_Parsed(t *testing.T) {
	raw := "```json\n{\"score\":85,\"verdict\":\"bullish\",\"confidence\":0.9,\"findings\":{\"revenue_growth\":\"15%\"}}\n```"
	parser := NewJSONParser[analystOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse markdown-wrapped JSON failed: %v", err)
	}
	report := result.ToReport("Fundamentals Analyst", "fundamentals")
	if err := report.Validate(); err != nil {
		t.Errorf("AnalystReport.Validate failed: %v", err)
	}
}

func TestBuildFundamentalsAnalyst_InvalidVerdict_Rejected(t *testing.T) {
	raw := `{"score":50,"verdict":"unknown","confidence":0.5}`
	var out analystOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	report := out.ToReport("Fundamentals Analyst", "fundamentals")
	if err := report.Validate(); err == nil {
		t.Error("AnalystReport.Validate should reject invalid verdict")
	}
}

// ─── Research Manager Prompt Alignment ────────────────────

func TestBuildResearchManagerPrompt_OutputMatchesSchema(t *testing.T) {
	state := &research.ResearchState{
		Symbol: "AAPL",
		DebateState: &research.InvestDebateState{
			BullArguments: []string{"Strong growth"},
			BearArguments: []string{"High valuation"},
		},
	}
	prompt := BuildResearchManagerPrompt(state)

	if !contains(prompt, "recommendation") {
		t.Error("prompt should mention recommendation")
	}
	if !contains(prompt, "rationale") {
		t.Error("prompt should mention rationale")
	}
	if !contains(prompt, "strategic_action") {
		t.Error("prompt should mention strategic_action")
	}
	if !contains(prompt, "Buy") || !contains(prompt, "Overweight") {
		t.Error("prompt should mention rating options")
	}
}

func TestBuildResearchManager_ValidJSON_ParsesToResearchPlan(t *testing.T) {
	raw := `{"recommendation":"Overweight","rationale":"Strong fundamentals.","strategic_action":"Accumulate on dips."}`
	var out researchPlanOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	plan := out.ToPlan()
	if err := plan.Validate(); err != nil {
		t.Errorf("ResearchPlan.Validate failed: %v", err)
	}
}

func TestBuildResearchManager_MarkdownWrappedJSON_Parsed(t *testing.T) {
	raw := "```json\n{\"recommendation\":\"Buy\",\"rationale\":\"Strong growth.\",\"strategic_action\":\"Buy now.\"}\n```"
	parser := NewJSONParser[researchPlanOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse markdown-wrapped JSON failed: %v", err)
	}
	plan := result.ToPlan()
	if err := plan.Validate(); err != nil {
		t.Errorf("ResearchPlan.Validate failed: %v", err)
	}
	if string(plan.Recommendation) != "Buy" {
		t.Errorf("expected Buy, got %s", plan.Recommendation)
	}
}

func TestBuildResearchManager_InvalidRating_Rejected(t *testing.T) {
	raw := `{"recommendation":"Moon","rationale":"test","strategic_action":"test"}`
	var out researchPlanOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	plan := out.ToPlan()
	if err := plan.Validate(); err == nil {
		t.Error("ResearchPlan.Validate should reject invalid rating")
	}
}

func TestBuildResearchManager_MissingRationale_Rejected(t *testing.T) {
	raw := `{"recommendation":"Buy","rationale":"","strategic_action":"test"}`
	var out researchPlanOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	plan := out.ToPlan()
	if err := plan.Validate(); err == nil {
		t.Error("ResearchPlan.Validate should reject empty rationale")
	}
}

// ─── Trader Prompt Alignment ──────────────────────────────

func TestBuildTraderPrompt_OutputMatchesSchema(t *testing.T) {
	state := &research.ResearchState{Symbol: "MSFT", ResearchPlan: &research.ResearchPlan{
		Recommendation:  research.RatingBuy,
		Rationale:       "Strong cloud",
		StrategicAction: "Accumulate",
	}}
	prompt := BuildTraderPrompt(state)

	if !contains(prompt, "action") {
		t.Error("prompt should mention action")
	}
	if !contains(prompt, "reasoning") {
		t.Error("prompt should mention reasoning")
	}
	if !contains(prompt, "entry_price") {
		t.Error("prompt should mention entry_price")
	}
	if !contains(prompt, "stop_loss") {
		t.Error("prompt should mention stop_loss")
	}
	if !contains(prompt, "position_sizing") {
		t.Error("prompt should mention position_sizing")
	}
}

func TestBuildTrader_ValidJSON_ParsesToTraderProposal(t *testing.T) {
	raw := `{"action":"buy","reasoning":"Strong setup.","entry_price":150.0,"stop_loss":140.0,"position_sizing":"5%"}`
	var out traderOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	prop := out.ToProposal()
	if err := prop.Validate(); err != nil {
		t.Errorf("TraderProposal.Validate failed: %v", err)
	}
}

func TestBuildTrader_MissingOptionalFields_Passes(t *testing.T) {
	raw := `{"action":"hold","reasoning":"Wait for better entry."}`
	var out traderOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	prop := out.ToProposal()
	if err := prop.Validate(); err != nil {
		t.Errorf("TraderProposal.Validate failed: %v", err)
	}
	if prop.EntryPrice != nil {
		t.Error("expected nil entry price")
	}
}

func TestBuildTrader_MarkdownWrappedJSON_Parsed(t *testing.T) {
	raw := "```json\n{\"action\":\"sell\",\"reasoning\":\"Take profit.\"}\n```"
	parser := NewJSONParser[traderOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse markdown-wrapped JSON failed: %v", err)
	}
	prop := result.ToProposal()
	if err := prop.Validate(); err != nil {
		t.Errorf("TraderProposal.Validate failed: %v", err)
	}
	if prop.Action != "sell" {
		t.Errorf("expected sell, got %s", prop.Action)
	}
}

func TestBuildTrader_MissingAction_Rejected(t *testing.T) {
	raw := `{"action":"","reasoning":"test"}`
	var out traderOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	prop := out.ToProposal()
	if err := prop.Validate(); err == nil {
		t.Error("TraderProposal.Validate should reject empty action")
	}
}

func TestBuildTrader_StopLossAboveEntry_Rejected(t *testing.T) {
	entry := 100.0
	stop := 150.0
	raw := traderOutput{
		Action:     "buy",
		Reasoning:  "test",
		EntryPrice: &entry,
		StopLoss:   &stop,
	}
	prop := raw.ToProposal()
	if err := prop.Validate(); err == nil {
		t.Error("TraderProposal.Validate should reject stop_loss > entry_price")
	}
}

// ─── Portfolio Manager Prompt Alignment ───────────────────

func TestBuildPortfolioManagerPrompt_OutputMatchesSchema(t *testing.T) {
	state := &research.ResearchState{Symbol: "AAPL"}
	prompt := BuildPortfolioManagerPrompt(state)

	if !contains(prompt, "rating") {
		t.Error("prompt should mention rating")
	}
	if !contains(prompt, "executive_summary") {
		t.Error("prompt should mention executive_summary")
	}
	if !contains(prompt, "investment_thesis") {
		t.Error("prompt should mention investment_thesis")
	}
	if !contains(prompt, "price_target") {
		t.Error("prompt should mention price_target")
	}
	if !contains(prompt, "time_horizon") {
		t.Error("prompt should mention time_horizon")
	}
}

func TestBuildPortfolioManager_ValidJSON_ParsesToPortfolioDecision(t *testing.T) {
	raw := `{"rating":"Overweight","executive_summary":"Strong buy.","investment_thesis":"AI tailwind.","price_target":250.0,"time_horizon":"12 months"}`
	var out portfolioDecisionOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	dec := out.ToDecision()
	if err := dec.Validate(); err != nil {
		t.Errorf("PortfolioDecision.Validate failed: %v", err)
	}
}

func TestBuildPortfolioManager_MissingOptionalPriceTarget_Passes(t *testing.T) {
	raw := `{"rating":"Hold","executive_summary":"Hold for now.","investment_thesis":"Wait for catalyst."}`
	var out portfolioDecisionOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	dec := out.ToDecision()
	if err := dec.Validate(); err != nil {
		t.Errorf("PortfolioDecision.Validate failed: %v", err)
	}
}

func TestBuildPortfolioManager_MarkdownWrappedJSON_Parsed(t *testing.T) {
	raw := "```json\n{\"rating\":\"Sell\",\"executive_summary\":\"Take profits.\",\"investment_thesis\":\"Overvalued.\"}\n```"
	parser := NewJSONParser[portfolioDecisionOutput]()
	result, err := parser.Parse(raw)
	if err != nil {
		t.Fatalf("parse markdown-wrapped JSON failed: %v", err)
	}
	dec := result.ToDecision()
	if err := dec.Validate(); err != nil {
		t.Errorf("PortfolioDecision.Validate failed: %v", err)
	}
	if string(dec.Rating) != "Sell" {
		t.Errorf("expected Sell, got %s", dec.Rating)
	}
}

func TestBuildPortfolioManager_InvalidRating_Rejected(t *testing.T) {
	raw := `{"rating":"StrongBuy","executive_summary":"test","investment_thesis":"test"}`
	var out portfolioDecisionOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	dec := out.ToDecision()
	if err := dec.Validate(); err == nil {
		t.Error("PortfolioDecision.Validate should reject invalid rating")
	}
}

func TestBuildPortfolioManager_MissingSummary_Rejected(t *testing.T) {
	raw := `{"rating":"Buy","executive_summary":"","investment_thesis":"test"}`
	var out portfolioDecisionOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	dec := out.ToDecision()
	if err := dec.Validate(); err == nil {
		t.Error("PortfolioDecision.Validate should reject empty summary")
	}
}

// ─── Bull/Bear Researcher Prompt Tests ───────────────────

func TestBuildBullResearcherPrompt_OutputFormat(t *testing.T) {
	state := &research.ResearchState{
		Symbol: "NVDA",
		AnalystReports: map[string]*research.AnalystReport{
			"market": {AnalystName: "Market Analyst", Score: 80, Verdict: "bullish", Confidence: 0.8},
		},
	}
	prompt := BuildBullResearcherPrompt(state)
	if !contains(prompt, "score") {
		t.Error("prompt should mention score")
	}
	if !contains(prompt, "thesis") {
		t.Error("prompt should mention thesis")
	}
	if !contains(prompt, "confidence") {
		t.Error("prompt should mention confidence")
	}
}

func TestBuildBearResearcherPrompt_OutputFormat(t *testing.T) {
	state := &research.ResearchState{
		Symbol: "NVDA",
		AnalystReports: map[string]*research.AnalystReport{
			"market": {AnalystName: "Market Analyst", Score: 30, Verdict: "bearish", Confidence: 0.6},
		},
	}
	prompt := BuildBearResearcherPrompt(state)
	if !contains(prompt, "score") {
		t.Error("prompt should mention score")
	}
	if !contains(prompt, "thesis") {
		t.Error("prompt should mention thesis")
	}
	if !contains(prompt, "confidence") {
		t.Error("prompt should mention confidence")
	}
}

// ─── Risk Analyst Prompt Tests ────────────────────────────

func TestBuildAggressiveRiskPrompt_OutputHasRiskFields(t *testing.T) {
	state := &research.ResearchState{Symbol: "AAPL", TraderProposal: &research.TraderProposal{Action: "buy", Reasoning: "test"}}
	prompt := BuildAggressiveRiskPrompt(state)
	if !contains(prompt, "risk_level") {
		t.Error("prompt should mention risk_level")
	}
	if !contains(prompt, "recommendation") {
		t.Error("prompt should mention recommendation")
	}
}

func TestBuildConservativeRiskPrompt_OutputHasRiskFields(t *testing.T) {
	state := &research.ResearchState{Symbol: "AAPL"}
	prompt := BuildConservativeRiskPrompt(state)
	if !contains(prompt, "capital_preservation_score") {
		t.Error("prompt should mention capital_preservation_score")
	}
	if !contains(prompt, "risk_level") {
		t.Error("prompt should mention risk_level")
	}
}

func TestBuildNeutralRiskPrompt_OutputHasRiskFields(t *testing.T) {
	state := &research.ResearchState{Symbol: "AAPL"}
	prompt := BuildNeutralRiskPrompt(state)
	if !contains(prompt, "var_estimate") {
		t.Error("prompt should mention var_estimate")
	}
	if !contains(prompt, "risk_level") {
		t.Error("prompt should mention risk_level")
	}
}

// ─── Edge Cases: Empty State Prompts ──────────────────────

func TestBuildMarketAnalystPrompt_EmptySymbol_NoPanic(t *testing.T) {
	state := &research.ResearchState{}
	prompt := BuildMarketAnalystPrompt(state)
	if prompt == "" {
		t.Error("prompt should not be empty even with empty symbol")
	}
}

func TestBuildResearchManagerPrompt_NoDebate_NoPanic(t *testing.T) {
	state := &research.ResearchState{Symbol: "TEST"}
	prompt := BuildResearchManagerPrompt(state)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

func TestBuildPortfolioManagerPrompt_NoRiskDebate_NoPanic(t *testing.T) {
	state := &research.ResearchState{Symbol: "TEST"}
	prompt := BuildPortfolioManagerPrompt(state)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

func TestBuildTraderPrompt_NoResearchPlan_NoPanic(t *testing.T) {
	state := &research.ResearchState{Symbol: "TEST"}
	prompt := BuildTraderPrompt(state)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

func TestBuildBullResearcherPrompt_NoReports_NoPanic(t *testing.T) {
	state := &research.ResearchState{Symbol: "TEST"}
	prompt := BuildBullResearcherPrompt(state)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}

func TestBuildBearResearcherPrompt_NoReports_NoPanic(t *testing.T) {
	state := &research.ResearchState{Symbol: "TEST"}
	prompt := BuildBearResearcherPrompt(state)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
}
