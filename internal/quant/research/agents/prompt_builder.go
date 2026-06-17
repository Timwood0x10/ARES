package agents

import (
	"fmt"

	"goagentx/internal/quant/research"
)

// PromptBuilder provides pure functions for constructing agent prompts.
// All functions are deterministic: same input always produces same output.
// No network access, file I/O, or global state is used.

// BuildMarketAnalystPrompt constructs the market analyst prompt.
// If state contains a VerifiedMarketSnapshot, it injects the snapshot data.
func BuildMarketAnalystPrompt(state *research.ResearchState) string {
	snapshotSection := ""
	if state.MarketSnapshot != nil {
		snapshotSection = formatSnapshot(state.MarketSnapshot, state.Symbol)
	}
	return fmt.Sprintf(`You are a Market Analyst. Your job is to analyze price action,
technical indicators, and market structure for %s.

%s
Analyze the following aspects:
1. Price trend and momentum
2. Key support and resistance levels
3. Volume patterns
4. Technical indicator signals (MACD, RSI, moving averages)
5. Overall market structure

Output as JSON:
{
  "ticker": "%s",
  "score": <1-10>,
  "trend": "<uptrend|downtrend|sideways>",
  "rsi_state": "<overbought|oversold|neutral>",
  "macd_signal": "<bullish|bearish|neutral>",
  "verdict": "<bullish|bearish|neutral>",
  "reasoning": "<detailed analysis>"
}

你是市场分析师。分析价格走势、技术指标和市场结构。
不要编造数据，只使用提供的数据。`, state.Symbol, snapshotSection, state.Symbol)
}

// BuildSentimentAnalystPrompt constructs the sentiment analyst prompt.
func BuildSentimentAnalystPrompt(state *research.ResearchState) string {
	return fmt.Sprintf(`You are a Sentiment Analyst. Gauge market sentiment for %s.

Analyze sentiment from these angles:
1. Prediction market probabilities if available
2. Social media and news sentiment trends
3. Options flow and unusual activity signals
4. Institutional vs retail positioning

Output as JSON:
{
  "ticker": "%s",
  "score": <0.0-1.0>,
  "band": "<strongly_bullish|bullish|neutral|bearish|strongly_bearish>",
  "confidence": <0.0-1.0>,
  "signals": ["<signal1>", "<signal2>"],
  "reasoning": "<sentiment analysis>"
}

你是情绪分析师。从预测市场、新闻情绪、期权流向等角度评估市场情绪。`, state.Symbol, state.Symbol)
}

// BuildNewsAnalystPrompt constructs the news analyst prompt.
func BuildNewsAnalystPrompt(state *research.ResearchState) string {
	return fmt.Sprintf(`You are a News Analyst. Assess recent news impact on %s.

Consider these categories:
1. Earnings reports and guidance changes
2. Regulatory developments
3. Macro-economic indicators
4. Industry-specific trends
5. Competitive landscape moves

Output as JSON:
{
  "ticker": "%s",
  "score": <1-10>,
  "positive": ["<positive factor1>", "<positive factor2>"],
  "negative": ["<negative factor1>", "<negative factor2>"],
  "topics": ["<topic1>", "<topic2>"],
  "verdict": "<bullish|bearish|neutral>",
  "reasoning": "<news impact analysis>"
}

你是新闻分析师。评估近期新闻和宏观事件对股票的影响。`, state.Symbol, state.Symbol)
}

// BuildFundamentalsAnalystPrompt constructs the fundamentals analyst prompt.
func BuildFundamentalsAnalystPrompt(state *research.ResearchState) string {
	return fmt.Sprintf(`You are a Fundamentals Analyst. Evaluate financial health of %s.

Analyze these fundamental metrics:
1. Revenue growth trends and trajectory
2. Profit margins (gross, operating, net)
3. Valuation ratios (P/E, P/B, P/S, EV/EBITDA)
4. Debt levels and interest coverage
5. Competitive position and moat

Output as JSON:
{
  "ticker": "%s",
  "score": <1-10>,
  "metrics": {
    "revenue_growth": "<value>",
    "pe_ratio": "<value>",
    "debt_to_equity": "<value>"
  },
  "strengths": ["<strength1>", "<strength2>"],
  "risks": ["<risk1>", "<risk2>"],
  "verdict": "<bullish|bearish|neutral>",
  "reasoning": "<fundamental assessment>"
}

你是基本面分析师。调用财务数据工具，评估股票的财务健康度。
不要编造数据——使用工具输出。`, state.Symbol, state.Symbol)
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
Output as JSON:
{
  "ticker": "%s",
  "score": <1-10>,
  "thesis": "<core bullish thesis>",
  "arguments": ["<argument1>", "<argument2>"],
  "target": "<price target or range>",
  "confidence": <0.0-1.0>
}

你是多头研究员。基于分析师报告构建最强的多头投资论点。`, state.Symbol, contextSection, debateHistory, state.Symbol)
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
Output as JSON:
{
  "ticker": "%s",
  "score": <1-10>,
  "thesis": "<core bearish thesis>",
  "arguments": ["<argument1>", "<argument2>"],
  "target": "<downside target or range>",
  "confidence": <0.0-1.0>
}

你是空头研究员。基于分析师报告构建最强的空头投资论点。`, state.Symbol, contextSection, debateHistory, state.Symbol)
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

Your role: Weigh arguments from both sides, identify consensus points, and produce
a clear investment recommendation with actionable steps.

%s
Output as JSON:
{
  "recommendation": "<Buy|Overweight|Hold|Underweight|Sell>",
  "rationale": "<balanced reasoning synthesizing both views>",
  "strategic_action": "<specific action items>"
}

你是研究经理。综合多空辩论，收敛成投资计划和评级建议。`, state.Symbol, debateSection)
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

Your role: Translate investment thesis into executable trade parameters.
Consider entry timing, position sizing, risk management, and exit strategy.

%s
Output as JSON:
{
  "action": "<buy|sell|hold>",
  "reasoning": "<trading rationale based on research plan>",
  "entry_price": <optional float>,
  "stop_loss": <optional float>,
  "position_sizing": "<sizing recommendation>"
}

你是交易员。将研究计划转化为具体的交易提案。`, state.Symbol, planSection)
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
Output as JSON:
{
  "risk_level": "<low|medium|high|critical>",
  "tail_risks": ["<risk1>", "<risk2>"],
  "max_drawdown_estimate": "<percentage>",
  "recommendation": "<proceed|modify|reject>",
  "reasoning": "<aggressive risk assessment>"
}

你是激进风险分析师。从战术角度评估尾部风险和极端情景。`, state.Symbol, traderSection)
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
Output as JSON:
{
  "risk_level": "<low|medium|high|critical>",
  "downside_risks": ["<risk1>", "<risk2>"],
  "capital_preservation_score": <1-10>,
  "recommendation": "<proceed|modify|reject>",
  "reasoning": "<conservative risk assessment>"
}

你是保守风险分析师。从审慎角度评估资本保护和下行风险。`, state.Symbol, traderSection)
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
Output as JSON:
{
  "risk_level": "<low|medium|high|critical>",
  "var_estimate": "<value-at-risk estimate>",
  "expected_shortfall": "<expected shortfall estimate>",
  "probability_weighted_return": "<range>",
  "recommendation": "<proceed|modify|reject>",
  "reasoning": "<quantitative risk assessment>"
}

你是中性风险分析师。从量化角度提供平衡的风险评估。`, state.Symbol, traderSection)
}

// BuildPortfolioManagerPrompt constructs the portfolio manager prompt.
// Injects risk debate history and past memory context if available.
func BuildPortfolioManagerPrompt(state *research.ResearchState) string {
	riskSection := buildRiskDebateContext(state)
	memorySection := buildMemoryContext(state)

	return fmt.Sprintf(`You are the Portfolio Manager. Make the final investment decision for %s.

Your role: Review all research outputs, weigh risk assessments, consider
historical context, and issue the final portfolio decision with conviction level.

%s
%s
Output as JSON:
{
  "rating": "<Buy|Overweight|Hold|Underweight|Sell>",
  "executive_summary": "<concise decision summary>",
  "investment_thesis": "<core thesis supporting decision>",
  "price_target": <optional float>,
  "time_horizon": "<short|medium|long term>"
}

你是投资组合经理。综合所有研究和风险评估，做出最终投资决策。`, state.Symbol, riskSection, memorySection)
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
	if reports == nil || len(reports) == 0 {
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
	// Memory context is injected externally via PMContext; this is a placeholder.
	// The actual memory injection happens at orchestration layer.
	return ""
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
