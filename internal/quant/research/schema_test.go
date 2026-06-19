package research

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── PortfolioRating Tests ──────────────────────────────────

func TestPortfolioRating_IsValid(t *testing.T) {
	tests := []struct {
		name   string
		rating PortfolioRating
		want   bool
	}{
		{"Buy is valid", RatingBuy, true},
		{"Overweight is valid", RatingOverweight, true},
		{"Hold is valid", RatingHold, true},
		{"Underweight is valid", RatingUnderweight, true},
		{"Sell is valid", RatingSell, true},
		{"Invalid rating", PortfolioRating("StrongBuy"), false},
		{"Empty rating", PortfolioRating(""), false},
		{"Random string", PortfolioRating("MAYBE"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rating.IsValid())
		})
	}
}

func TestParsePortfolioRating_Valid(t *testing.T) {
	tests := []struct {
		input string
		want  PortfolioRating
	}{
		{"Buy", RatingBuy},
		{"Overweight", RatingOverweight},
		{"Hold", RatingHold},
		{"Underweight", RatingUnderweight},
		{"Sell", RatingSell},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParsePortfolioRating(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParsePortfolioRating_Invalid(t *testing.T) {
	_, err := ParsePortfolioRating("StrongBuy")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid portfolio rating")

	_, err = ParsePortfolioRating("")
	assert.Error(t, err)
}

func TestPortfolioRating_String(t *testing.T) {
	assert.Equal(t, "Buy", RatingBuy.String())
	assert.Equal(t, "Sell", RatingSell.String())
}

// ─── ResearchPlan Tests ─────────────────────────────────────

func TestResearchPlan_Validate_OK(t *testing.T) {
	p := &ResearchPlan{
		Recommendation:  RatingBuy,
		Rationale:       "Strong fundamentals and growth prospects.",
		StrategicAction: "Accumulate on dips.",
	}
	assert.NoError(t, p.Validate())
}

func TestResearchPlan_Validate_Nil(t *testing.T) {
	err := (*ResearchPlan)(nil).Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestResearchPlan_Validate_InvalidRating(t *testing.T) {
	p := &ResearchPlan{
		Recommendation:  PortfolioRating("Maybe"),
		Rationale:       "test",
		StrategicAction: "test",
	}
	err := p.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid recommendation")
}

func TestResearchPlan_Validate_EmptyFields(t *testing.T) {
	p := &ResearchPlan{Recommendation: RatingHold}
	err := p.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rationale")

	p2 := &ResearchPlan{Recommendation: RatingHold, Rationale: "ok"}
	err = p2.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "strategic action")
}

// ─── TraderProposal Tests ───────────────────────────────────

func TestTraderProposal_Validate_OK(t *testing.T) {
	ep, sl := 150.0, 140.0
	tp := &TraderProposal{
		Action:     "BUY",
		Reasoning:  "Breakout confirmed.",
		EntryPrice: &ep,
		StopLoss:   &sl,
	}
	assert.NoError(t, tp.Validate())
}

func TestTraderProposal_Validate_Nil(t *testing.T) {
	err := (*TraderProposal)(nil).Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestTraderProposal_Validate_EmptyAction(t *testing.T) {
	tp := &TraderProposal{Reasoning: "test"}
	err := tp.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "action")
}

func TestTraderProposal_Validate_NegativeEntryPrice(t *testing.T) {
	ep := -10.0
	tp := &TraderProposal{Action: "buy", Reasoning: "test", EntryPrice: &ep}
	err := tp.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "entry price must be positive")
}

func TestTraderProposal_Validate_StopLossAboveEntry(t *testing.T) {
	ep, sl := 100.0, 110.0
	tp := &TraderProposal{Action: "buy", Reasoning: "test", EntryPrice: &ep, StopLoss: &sl}
	err := tp.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "stop loss")
}

func TestTraderProposal_Validate_OptionalFieldsOK(t *testing.T) {
	tp := &TraderProposal{Action: "HOLD", Reasoning: "Wait for better entry."}
	assert.NoError(t, tp.Validate())
}

// ─── PortfolioDecision Tests ────────────────────────────────

func TestPortfolioDecision_Validate_OK(t *testing.T) {
	pt := 250.0
	pd := &PortfolioDecision{
		Rating:           RatingOverweight,
		ExecutiveSummary: "Strong Q3 earnings beat expectations.",
		InvestmentThesis: "AI-driven growth accelerates revenue.",
		PriceTarget:      &pt,
		TimeHorizon:      "12 months",
	}
	assert.NoError(t, pd.Validate())
}

func TestPortfolioDecision_Validate_Nil(t *testing.T) {
	err := (*PortfolioDecision)(nil).Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestPortfolioDecision_Validate_InvalidRating(t *testing.T) {
	pd := &PortfolioDecision{
		Rating:           PortfolioRating("Accumulate"),
		ExecutiveSummary: "test",
		InvestmentThesis: "test",
	}
	err := pd.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid rating")
}

func TestPortfolioDecision_Validate_NegativePriceTarget(t *testing.T) {
	pt := -50.0
	pd := &PortfolioDecision{
		Rating:           RatingHold,
		ExecutiveSummary: "test",
		InvestmentThesis: "test",
		PriceTarget:      &pt,
	}
	err := pd.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "price target must be positive")
}

// ─── AnalystReport Tests ────────────────────────────────────

func TestAnalystReport_Validate_OK(t *testing.T) {
	ar := &AnalystReport{
		AnalystName: "MarketAnalyst",
		AnalystType: "market",
		Score:       75.5,
		Verdict:     "buy",
		Confidence:  0.85,
		Timestamp:   time.Now(),
	}
	assert.NoError(t, ar.Validate())
}

func TestAnalystReport_Validate_Nil(t *testing.T) {
	err := (*AnalystReport)(nil).Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestAnalystReport_Validate_EmptyName(t *testing.T) {
	ar := &AnalystReport{AnalystType: "market", Score: 50, Verdict: "hold", Confidence: 0.5}
	err := ar.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "analyst name")
}

func TestAnalystReport_Validate_InvalidType(t *testing.T) {
	ar := &AnalystReport{AnalystName: "X", AnalystType: "quant", Score: 50, Verdict: "hold", Confidence: 0.5}
	err := ar.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid analyst type")
}

func TestAnalystReport_Validate_ScoreRange(t *testing.T) {
	ar := &AnalystReport{AnalystName: "X", AnalystType: "market", Score: -1, Verdict: "hold", Confidence: 0.5}
	err := ar.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "score must be in [0,100]")

	ar2 := &AnalystReport{AnalystName: "X", AnalystType: "market", Score: 101, Verdict: "hold", Confidence: 0.5}
	err = ar2.Validate()
	assert.Error(t, err)
}

func TestAnalystReport_Validate_ConfidenceRange(t *testing.T) {
	ar := &AnalystReport{AnalystName: "X", AnalystType: "market", Score: 50, Verdict: "hold", Confidence: 1.5}
	err := ar.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "confidence must be in [0,1]")
}

func TestAnalystReport_Validate_InvalidVerdict(t *testing.T) {
	ar := &AnalystReport{AnalystName: "X", AnalystType: "market", Score: 50, Verdict: "garbage_value", Confidence: 0.5}
	err := ar.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid verdict")
}

func TestAnalystReport_Validate_EmptyVerdict(t *testing.T) {
	ar := &AnalystReport{AnalystName: "X", AnalystType: "market", Score: 50, Verdict: "", Confidence: 0.5}
	err := ar.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "verdict must not be empty")
}

// ─── SentimentReport Tests ──────────────────────────────────

func TestSentimentReport_Validate_OK(t *testing.T) {
	sr := &SentimentReport{
		Band:       "bullish",
		Score:      0.65,
		Confidence: 0.8,
		Signals:    []string{"MACD bullish crossover"},
	}
	assert.NoError(t, sr.Validate())
}

func TestSentimentReport_Validate_Nil(t *testing.T) {
	err := (*SentimentReport)(nil).Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestSentimentReport_Validate_InvalidBand(t *testing.T) {
	sr := &SentimentReport{Band: "super_bullish", Score: 0.7, Confidence: 0.8}
	err := sr.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid sentiment band")
}

func TestSentimentReport_Validate_ScoreOutOfRange(t *testing.T) {
	sr := &SentimentReport{Band: "neutral", Score: 1.5, Confidence: 0.8}
	err := sr.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sentiment score must be in [0,1]")
}

// ─── VerifiedMarketSnapshot Tests ───────────────────────────

func TestVerifiedMarketSnapshot_Validate_OK(t *testing.T) {
	vs := &VerifiedMarketSnapshot{
		OHLCV:        &Candle{Ticker: "AAPL", Close: 180.0},
		RecentCloses: []float64{175, 178, 179, 180, 181},
	}
	assert.NoError(t, vs.Validate())
}

func TestVerifiedMarketSnapshot_Validate_Nil(t *testing.T) {
	err := (*VerifiedMarketSnapshot)(nil).Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestVerifiedMarketSnapshot_Validate_MissingOHLCV(t *testing.T) {
	vs := &VerifiedMarketSnapshot{RecentCloses: []float64{100}}
	err := vs.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "OHLCV data is missing")
}

func TestVerifiedMarketSnapshot_Validate_EmptyCloses(t *testing.T) {
	vs := &VerifiedMarketSnapshot{OHLCV: &Candle{Ticker: "AAPL"}, RecentCloses: []float64{}}
	err := vs.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "recent closes are empty")
}

// ─── Render Tests ───────────────────────────────────────────

func TestRenderMarkdown_ResearchPlan(t *testing.T) {
	rp := &ResearchPlan{
		Recommendation:  RatingBuy,
		Rationale:       "Strong growth ahead.",
		StrategicAction: "Accumulate position.",
	}
	md := RenderMarkdown(rp)
	assert.Contains(t, md, "## Research Plan")
	assert.Contains(t, md, "**Recommendation:** Buy")
	assert.Contains(t, md, "Strong growth ahead.")
	assert.Contains(t, md, "Accumulate position.")
}

func TestRenderMarkdown_TraderProposal(t *testing.T) {
	ep, sl := 200.0, 190.0
	tp := &TraderProposal{
		Action:         "BUY",
		Reasoning:      "Breakout pattern.",
		EntryPrice:     &ep,
		StopLoss:       &sl,
		PositionSizing: "5% of portfolio",
	}
	md := RenderMarkdownTP(tp)
	assert.Contains(t, md, "## Trader Proposal")
	assert.Contains(t, md, "**Action:** BUY")
	assert.Contains(t, md, "200.00")
	assert.Contains(t, md, "190.00")
	assert.Contains(t, md, "5% of portfolio")
}

func TestRenderMarkdown_PortfolioDecision(t *testing.T) {
	pt := 300.0
	pd := &PortfolioDecision{
		Rating:           RatingOverweight,
		ExecutiveSummary: "Solid earnings.",
		InvestmentThesis: "AI tailwinds continue.",
		PriceTarget:      &pt,
		TimeHorizon:      "12M",
	}
	md := RenderMarkdownPD(pd)
	assert.Contains(t, md, "## Portfolio Decision")
	assert.Contains(t, md, "**Rating:** Overweight")
	assert.Contains(t, md, "300.00")
	assert.Contains(t, md, "12M")
}

func TestRenderMarkdown_AnalystReport(t *testing.T) {
	ar := &AnalystReport{
		AnalystName: "MarketAnalyst",
		AnalystType: "market",
		Score:       80.0,
		Verdict:     "Bullish.",
		Confidence:  0.9,
		Timestamp:   time.Now(),
	}
	md := RenderMarkdownAR(ar)
	assert.Contains(t, md, "## Analyst Report: MarketAnalyst")
	assert.Contains(t, md, "**Type:** market")
	assert.Contains(t, md, "80.0/100")
}

func TestRenderMarkdown_NilInput(t *testing.T) {
	assert.Equal(t, "", RenderMarkdown(nil))
	assert.Equal(t, "", RenderMarkdownTP(nil))
	assert.Equal(t, "", RenderMarkdownPD(nil))
	assert.Equal(t, "", RenderMarkdownAR(nil))
}

// ─── ParseRatingFromMarkdown Tests ──────────────────────────

func TestParseRatingFromMarkdown_OK(t *testing.T) {
	pt := 250.0
	pd := &PortfolioDecision{
		Rating:           RatingUnderweight,
		ExecutiveSummary: "Caution warranted.",
		InvestmentThesis: "Valuation stretched.",
		PriceTarget:      &pt,
	}
	md := RenderMarkdownPD(pd)
	rating, err := ParseRatingFromMarkdown(md)
	require.NoError(t, err)
	assert.Equal(t, RatingUnderweight, rating)
}

func TestParseRatingFromMarkdown_NotFound(t *testing.T) {
	_, err := ParseRatingFromMarkdown("no rating here")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "rating header not found")
}

func TestParseRatingFromMarkdown_Empty(t *testing.T) {
	_, err := ParseRatingFromMarkdown("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty markdown input")
}

func TestParseRatingFromMarkdown_AllRatings(t *testing.T) {
	for _, r := range []PortfolioRating{RatingBuy, RatingOverweight, RatingHold, RatingUnderweight, RatingSell} {
		t.Run(r.String(), func(t *testing.T) {
			pt := 100.0
			pd := &PortfolioDecision{
				Rating:           r,
				ExecutiveSummary: "Test summary.",
				InvestmentThesis: "Test thesis.",
				PriceTarget:      &pt,
			}
			md := RenderMarkdownPD(pd)
			got, err := ParseRatingFromMarkdown(md)
			require.NoError(t, err)
			assert.Equal(t, r, got)
		})
	}
}

// ─── ResearchState Tests ─────────────────────────────────────

func TestNewResearchState(t *testing.T) {
	cfg := &ResearchConfig{MaxDebateRounds: 5, MaxRiskRounds: 3}
	state := NewResearchState("AAPL", time.Now().UTC(), cfg)
	assert.Equal(t, "AAPL", state.Symbol)
	assert.NotNil(t, state.AnalystReports)
	assert.NotNil(t, state.DebateState)
	assert.Equal(t, 5, state.DebateState.MaxRounds)
	assert.Equal(t, 3, state.RiskDebateState.MaxRounds)
	assert.Empty(t, state.StepsCompleted)
	assert.Nil(t, state.Error)
}

func TestNewResearchState_DefaultConfig(t *testing.T) {
	state := NewResearchState("MSFT", time.Now().UTC(), nil)
	assert.Equal(t, 3, state.DebateState.MaxRounds)
	assert.Equal(t, 2, state.RiskDebateState.MaxRounds)
}

func TestResearchState_Reset(t *testing.T) {
	cfg := &ResearchConfig{}
	state := NewResearchState("AAPL", time.Now().UTC(), cfg)
	state.CurrentStep = "done"
	state.StepsCompleted = []string{"step1"}
	state.ResearchPlan = &ResearchPlan{Recommendation: RatingBuy, Rationale: "x", StrategicAction: "y"}

	state.Reset()

	assert.Equal(t, "", state.CurrentStep)
	assert.Empty(t, state.StepsCompleted)
	assert.Nil(t, state.ResearchPlan)
	assert.Nil(t, state.TraderProposal)
	assert.Nil(t, state.PortfolioDecision)
	assert.Nil(t, state.MarketSnapshot)
	assert.Nil(t, state.Error)
	// Symbol and config preserved.
	assert.Equal(t, "AAPL", state.Symbol)
	assert.NotNil(t, state.Config)
}

func TestResearchState_CloneIndependence(t *testing.T) {
	original := NewResearchState("AAPL", time.Now().UTC(), nil)
	original.StepsCompleted = append(original.StepsCompleted, "step1")
	original.AnalystReports["market"] = &AnalystReport{
		AnalystName: "MarketAnalyst",
		AnalystType: "market",
		Score:       75,
		Confidence:  0.8,
	}

	cloned := original.Clone()

	// Mutate original.
	original.StepsCompleted = append(original.StepsCompleted, "step2")
	original.AnalystReports["market"].Score = 99

	// Clone should be unaffected.
	assert.Len(t, cloned.StepsCompleted, 1)
	assert.Equal(t, float64(75), cloned.AnalystReports["market"].Score)
}

func TestResearchState_CloneNil(t *testing.T) {
	assert.Nil(t, (*ResearchState)(nil).Clone())
}

func TestResearchState_ToJSON_FromJSON_RoundTrip(t *testing.T) {
	original := NewResearchState("GOOG", time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC), nil)
	original.CurrentStep = "trader"
	original.StepsCompleted = []string{"market", "sentiment"}

	data, err := original.ToJSON()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	restored, err := FromJSON(data)
	require.NoError(t, err)
	assert.Equal(t, original.Symbol, restored.Symbol)
	assert.Equal(t, original.CurrentStep, restored.CurrentStep)
	assert.Equal(t, original.StepsCompleted, restored.StepsCompleted)
}

func TestResearchState_FromJSON_Invalid(t *testing.T) {
	_, err := FromJSON([]byte("invalid json"))
	assert.Error(t, err)
}

func TestResearchState_ToJSON_Nil(t *testing.T) {
	data, err := (*ResearchState)(nil).ToJSON()
	require.NoError(t, err)
	// json.Marshal(nil) produces "null", ensure no panic.
	assert.NotNil(t, data)
}

// ─── InvestDebateState / RiskDebateState Tests ──────────────

func TestDefaultMaxRounds(t *testing.T) {
	cfg := &ResearchConfig{MaxDebateRounds: 10}
	assert.Equal(t, 10, defaultMaxRounds(cfg))
	assert.Equal(t, 3, defaultMaxRounds(nil))

	rcfg := &ResearchConfig{MaxRiskRounds: 5}
	assert.Equal(t, 5, defaultMaxRiskRounds(rcfg))
	assert.Equal(t, 2, defaultMaxRiskRounds(nil))
}
