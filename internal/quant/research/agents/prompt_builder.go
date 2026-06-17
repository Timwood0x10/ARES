package agents

import (
	"fmt"

	"goagentx/internal/quant/research"
)

// PromptBuilder provides pure functions for constructing agent prompts.
// All functions are deterministic: same input always produces same output.
// No network access, file I/O, or global state is used.

// BuildMarketAnalystPrompt constructs the market analyst prompt.
// Output JSON matches the AnalystReport schema (score 0-100, findings for domain data).
// If state contains a VerifiedMarketSnapshot, it injects the snapshot data.
func BuildMarketAnalystPrompt(state *research.ResearchState) string {
	snapshotSection := ""
	if state.MarketSnapshot != nil {
		snapshotSection = formatSnapshot(state.MarketSnapshot, state.Symbol)
	}
	return fmt.Sprintf(`You are a Market Analyst. Analyze price action, technical indicators, and market structure for %s.

%s
Analyze these aspects:
1. Price trend and momentum
2. Key support and resistance levels
3. Volume patterns
4. Technical indicator signals (MACD, RSI, moving averages)
5. Overall market structure

Output valid JSON with these fields:
{
  "score": <0-100>,
  "verdict": "<bullish|bearish|neutral>",
  "confidence": <0.0-1.0>,
  "findings": {
    "trend": "<uptrend|downtrend|sideways>",
    "rsi_state": "<overbought|oversold|neutral>",
    "macd_signal": "<bullish|bearish|neutral>",
    "reasoning": "<detailed technical analysis>"
  }
}

Do not fabricate data; use only the provided data.`, state.Symbol, snapshotSection)
}

// BuildSentimentAnalystPrompt constructs the sentiment analyst prompt.
// Output JSON matches the SentimentReport schema (band, score 0-1, confidence, signals).
func BuildSentimentAnalystPrompt(state *research.ResearchState) string {
	return fmt.Sprintf(`You are a Sentiment Analyst. Gauge market sentiment for %s.

Analyze sentiment from these angles:
1. Prediction market probabilities if available
2. Social media and news sentiment trends
3. Options flow and unusual activity signals
4. Institutional vs retail positioning

Output valid JSON with these fields:
{
  "band": "<strongly_bullish|bullish|neutral|bearish|strongly_bearish>",
  "score": <0.0-1.0>,
  "confidence": <0.0-1.0>,
  "signals": ["<signal1>", "<signal2>"]
}`, state.Symbol)
}

// BuildNewsAnalystPrompt constructs the news analyst prompt.
// Output JSON matches the AnalystReport schema (score 0-100, findings for domain data).
func BuildNewsAnalystPrompt(state *research.ResearchState) string {
	return fmt.Sprintf(`You are a News Analyst. Assess recent news impact on %s.

Consider these categories:
1. Earnings reports and guidance changes
2. Regulatory developments
3. Macro-economic indicators
4. Industry-specific trends
5. Competitive landscape moves

Output valid JSON with these fields:
{
  "score": <0-100>,
  "verdict": "<bullish|bearish|neutral>",
  "confidence": <0.0-1.0>,
  "findings": {
    "positive_factors": ["<positive factor1>", "<positive factor2>"],
    "negative_factors": ["<negative factor1>", "<negative factor2>"],
    "topics": ["<topic1>", "<topic2>"],
    "reasoning": "<news impact analysis>"
  }
}`, state.Symbol)
}

// BuildFundamentalsAnalystPrompt constructs the fundamentals analyst prompt.
// Output JSON matches the AnalystReport schema (score 0-100, findings for domain data).
func BuildFundamentalsAnalystPrompt(state *research.ResearchState) string {
	return fmt.Sprintf(`You are a Fundamentals Analyst. Evaluate financial health of %s.

Analyze these fundamental metrics:
1. Revenue growth trends and trajectory
2. Profit margins (gross, operating, net)
3. Valuation ratios (P/E, P/B, P/S, EV/EBITDA)
4. Debt levels and interest coverage
5. Competitive position and moat

Output valid JSON with these fields:
{
  "score": <0-100>,
  "verdict": "<bullish|bearish|neutral>",
  "confidence": <0.0-1.0>,
  "findings": {
    "revenue_growth": "<value>",
    "pe_ratio": "<value>",
    "debt_to_equity": "<value>",
    "strengths": ["<strength1>", "<strength2>"],
    "risks": ["<risk1>", "<risk2>"],
    "reasoning": "<fundamental assessment>"
  }
}

Do not fabricate data.`, state.Symbol)
}

// BuildBullResearcherPrompt constructs the bull researcher prompt.
// Injects the 4 analyst reports if available in state.
func BuildBullResearcherPrompt(state *research.ResearchState) string {
	contextSection := buildAnalystContext(state)

	debateHistory := ""
	if state.DebateState != nil && len(state.DebateState.BullArguments) > 0 {
		debateHistory = formatDebateHistory("BULL", state.DebateState.BullArguments, state.DebateState.BearArguments)
	}

	return fmt.Sprintf(`You are a Bull Researcher. Build the strongest BULLISH investment case for %s.

Your role: Construct the most compelling long thesis using available evidence.
Be rigorous but advocate for the bullish view.

%s
%s
Output valid JSON with these fields:
{
  "score": <0-100>,
  "thesis": "<core bullish thesis>",
  "arguments": ["<argument1>", "<argument2>"],
  "target": "<price target or range>",
  "confidence": <0.0-1.0>
}`, state.Symbol, contextSection, debateHistory)
}

// BuildBearResearcherPrompt constructs the bear researcher prompt.
// Injects the 4 analyst reports if available in state.
func BuildBearResearcherPrompt(state *research.ResearchState) string {
	contextSection := buildAnalystContext(state)

	debateHistory := ""
	if state.DebateState != nil && len(state.DebateState.BearArguments) > 0 {
		debateHistory = formatDebateHistory("BEAR", state.DebateState.BearArguments, state.DebateState.BullArguments)
	}

	return fmt.Sprintf(`You are a Bear Researcher. Build the strongest BEARISH investment case for %s.

Your role: Construct the most compelling short thesis using available evidence.
Be rigorous but advocate for the bearish view.

%s
%s
Output valid JSON with these fields:
{
  "score": <0-100>,
  "thesis": "<core bearish thesis>",
  "arguments": ["<argument1>", "<argument2>"],
  "target": "<downside target or range>",
  "confidence": <0.0-1.0>
}`, state.Symbol, contextSection, debateHistory)
}

// BuildResearchManagerPrompt constructs the research manager prompt.
// Injects bull/bear debate history if available.
func BuildResearchManagerPrompt(state *research.ResearchState) string {
	debateSection := ""

	if state.DebateState != nil {
		bullArgs := state.DebateState.BullArguments
		bearArgs := state.DebateState.BearArguments

		if len(bullArgs) > 0 || len(bearArgs) > 0 {
			debateSection = "\n## Debate History\n\n"
			for i := 0; i < max(len(bullArgs), len(bearArgs)); i++ {
				debateSection += fmt.Sprintf("### Round %d\n", i+1)
				if i < len(bullArgs) {
					debateSection += fmt.Sprintf("**Bull:** %s\n", bullArgs[i])
				}
				if i < len(bearArgs) {
					debateSection += fmt.Sprintf("**Bear:** %s\n", bearArgs[i])
				}
			}
		}
	}

	return fmt.Sprintf(`You are the Research Manager. Synthesize the bull/bear debate into a coherent research plan for %s.

Weigh arguments from both sides, identify consensus points, and produce a clear investment recommendation with actionable steps.

%s
Output valid JSON with these fields:
{
  "recommendation": "<Buy|Overweight|Hold|Underweight|Sell>",
  "rationale": "<balanced reasoning synthesizing both views>",
  "strategic_action": "<specific action items>"
}`, state.Symbol, debateSection)
}

// BuildTraderPrompt constructs the trader prompt.
// Injects the research plan if available in state.
func BuildTraderPrompt(state *research.ResearchState) string {
	planSection := ""
	if state.ResearchPlan != nil {
		planSection = fmt.Sprintf(`
## Current Research Plan
- Recommendation: %s
- Rationale: %s
- Strategic Action: %s
`, state.ResearchPlan.Recommendation, state.ResearchPlan.Rationale, state.ResearchPlan.StrategicAction)
	}

	return fmt.Sprintf(`You are a Trader. Convert the research plan into a concrete trading proposal for %s.

Translate investment thesis into executable trade parameters.
Consider entry timing, position sizing, risk management, and exit strategy.

%s
Output valid JSON with these fields:
{
  "action": "<buy|sell|hold>",
  "reasoning": "<trading rationale based on research plan>",
  "entry_price": <optional float>,
  "stop_loss": <optional float>,
  "position_sizing": "<sizing recommendation>"
}`, state.Symbol, planSection)
}

// BuildAggressiveRiskPrompt constructs the aggressive risk analyst prompt.
func BuildAggressiveRiskPrompt(state *research.ResearchState) string {
	traderSection := ""
	if state.TraderProposal != nil {
		traderSection = fmt.Sprintf(`
## Proposed Trade
- Action: %s
- Reasoning: %s
`, state.TraderProposal.Action, state.TraderProposal.Reasoning)
	}

	return fmt.Sprintf(`You are an Aggressive Risk Analyst. Assess risks from an aggressive/tactical perspective for %s.

Focus on: tail risks, black swan scenarios, maximum drawdown potential,
liquidity risk, and correlation breakdown scenarios.

%s
Output valid JSON with these fields:
{
  "risk_level": "<low|medium|high|critical>",
  "tail_risks": ["<risk1>", "<risk2>"],
  "max_drawdown_estimate": "<percentage>",
  "recommendation": "<proceed|modify|reject>",
  "reasoning": "<aggressive risk assessment>"
}`, state.Symbol, traderSection)
}

// BuildConservativeRiskPrompt constructs the conservative risk analyst prompt.
func BuildConservativeRiskPrompt(state *research.ResearchState) string {
	traderSection := ""
	if state.TraderProposal != nil {
		traderSection = fmt.Sprintf(`
## Proposed Trade
- Action: %s
- Reasoning: %s
`, state.TraderProposal.Action, state.TraderProposal.Reasoning)
	}

	return fmt.Sprintf(`You are a Conservative Risk Analyst. Assess risks from a conservative/prudent perspective for %s.

Focus on: capital preservation, downside protection, regulatory risk,
valuation risk, and long-term sustainability.

%s
Output valid JSON with these fields:
{
  "risk_level": "<low|medium|high|critical>",
  "downside_risks": ["<risk1>", "<risk2>"],
  "capital_preservation_score": <0-100>,
  "recommendation": "<proceed|modify|reject>",
  "reasoning": "<conservative risk assessment>"
}`, state.Symbol, traderSection)
}

// BuildNeutralRiskPrompt constructs the neutral risk analyst prompt.
func BuildNeutralRiskPrompt(state *research.ResearchState) string {
	traderSection := ""
	if state.TraderProposal != nil {
		traderSection = fmt.Sprintf(`
## Proposed Trade
- Action: %s
- Reasoning: %s
`, state.TraderProposal.Action, state.TraderProposal.Reasoning)
	}

	return fmt.Sprintf(`You are a Neutral Risk Analyst. Provide balanced risk assessment for %s.

Focus on: quantitative risk metrics, volatility-adjusted returns,
Sharpe/sortino ratios, probability-weighted outcomes, and scenario analysis.

%s
Output valid JSON with these fields:
{
  "risk_level": "<low|medium|high|critical>",
  "var_estimate": "<value-at-risk estimate>",
  "expected_shortfall": "<expected shortfall estimate>",
  "probability_weighted_return": "<range>",
  "recommendation": "<proceed|modify|reject>",
  "reasoning": "<quantitative risk assessment>"
}`, state.Symbol, traderSection)
}

// BuildPortfolioManagerPrompt constructs the portfolio manager prompt.
// Injects risk debate history and past memory context if available.
func BuildPortfolioManagerPrompt(state *research.ResearchState) string {
	riskSection := buildRiskDebateContext(state)
	memorySection := buildMemoryContext(state)

	return fmt.Sprintf(`You are the Portfolio Manager. Make the final investment decision for %s.

Review all research outputs, weigh risk assessments, consider historical context, and issue the final portfolio decision with conviction level.

%s
%s
Output valid JSON with these fields:
{
  "rating": "<Buy|Overweight|Hold|Underweight|Sell>",
  "executive_summary": "<concise decision summary>",
  "investment_thesis": "<core thesis supporting decision>",
  "price_target": <optional float>,
  "time_horizon": "<short|medium|long term>"
}`, state.Symbol, riskSection, memorySection)
}

// ─── Internal Helpers ──────────────────────────────────────

func formatSnapshot(snapshot *research.VerifiedMarketSnapshot, symbol string) string {
	if snapshot == nil {
		return ""
	}
	indicatorStr := ""
	if snapshot.Indicators != nil {
		for k, v := range snapshot.Indicators {
			indicatorStr += fmt.Sprintf("- %s: %.2f\n", k, v)
		}
	}
	closesStr := ""
	if len(snapshot.RecentCloses) > 0 {
		closesStr = "Recent closes: "
		for _, c := range snapshot.RecentCloses {
			closesStr += fmt.Sprintf("%.2f ", c)
		}
	}
	warningStr := ""
	if snapshot.Warning != "" {
		warningStr = fmt.Sprintf("\n⚠ WARNING: %s\n", snapshot.Warning)
	}
	return fmt.Sprintf(`
## Verified Market Snapshot
- Symbol: %s
- Requested Date: %s
- Latest Data Date: %s
%s
%s
Indicators:
%s%s
`, symbol, snapshot.RequestedDate.Format("2006-01-02"),
		snapshot.LatestRowDate.Format("2006-01-02"),
		formatOHLCV(snapshot.OHLCV), closesStr, indicatorStr, warningStr)
}

func formatOHLCV(candle *research.Candle) string {
	if candle == nil {
		return "No OHLCV data available."
	}
	return fmt.Sprintf("O:%.2f H:%.2f L:%.2f C:%.2f V:%d",
		candle.Open, candle.High, candle.Low, candle.Close, candle.Volume)
}

func buildAnalystContext(state *research.ResearchState) string {
	var ctx string
	reports := state.AnalystReports
	if len(reports) == 0 {
		return ""
	}
	ctx = "\n## Analyst Reports\n\n"
	names := []string{"fundamentals", "sentiment", "news", "technical"}
	labels := map[string]string{
		"fundamentals": "Fundamentals Analyst",
		"sentiment":    "Sentiment Analyst",
		"news":         "News Analyst",
		"technical":    "Technical / Market Analyst",
	}
	for _, name := range names {
		if r, ok := reports[name]; ok && r != nil {
			ctx += fmt.Sprintf("### %s\n- Score: %.1f\n- Verdict: %s\n- Confidence: %.2f\n\n",
				labels[name], r.Score, r.Verdict, r.Confidence)
		}
	}
	return ctx
}

func formatDebateHistory(side string, myArgs, theirArgs []string) string {
	if len(myArgs) == 0 {
		return ""
	}
	history := "\n## Previous Debate Rounds\n\n"
	for i, arg := range myArgs {
		history += fmt.Sprintf("**%s (Round %d):** %s\n", side, i+1, arg)
		if i < len(theirArgs) {
			history += fmt.Sprintf("**Opponent (Round %d):** %s\n", i+1, theirArgs[i])
		}
	}
	return history
}

func buildRiskDebateContext(state *research.ResearchState) string {
	if state.RiskDebateState == nil {
		return ""
	}
	rd := state.RiskDebateState
	ctx := "\n## Risk Assessment Summary\n\n"
	if rd.AggressiveView != "" {
		ctx += fmt.Sprintf("**Aggressive View:** %s\n", rd.AggressiveView)
	}
	if rd.ConservativeView != "" {
		ctx += fmt.Sprintf("**Conservative View:** %s\n", rd.ConservativeView)
	}
	if rd.NeutralView != "" {
		ctx += fmt.Sprintf("**Neutral View:** %s\n", rd.NeutralView)
	}
	ctx += fmt.Sprintf("(Round %d/%d)\n", rd.Round, rd.MaxRounds)
	return ctx
}

func buildMemoryContext(state *research.ResearchState) string {
	if state.MemoryContext == nil {
		return ""
	}
	mc := state.MemoryContext
	ctx := "\n## Historical Memory Context\n\n"
	if mc.SameTickerSummary != "" {
		ctx += fmt.Sprintf("### Past Decisions for %s\n%s\n", state.Symbol, mc.SameTickerSummary)
	}
	if mc.AvgAccuracy != 0 {
		ctx += fmt.Sprintf("Average decision accuracy (alpha): %.2f%%\n\n", mc.AvgAccuracy)
	}
	if len(mc.CrossTickerLessons) > 0 {
		ctx += "### Cross-Ticker Lessons\n"
		for _, lesson := range mc.CrossTickerLessons {
			ctx += fmt.Sprintf("- %s\n", lesson)
		}
		ctx += "\n"
	}
	return ctx
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
