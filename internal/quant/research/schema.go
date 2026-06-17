package research

import (
	"fmt"
	"strings"
	"time"

	"goagentx/internal/quant/market"
)

// ─── PortfolioRating ───────────────────────────────────────

// PortfolioRating represents a 5-tier investment rating.
type PortfolioRating string

const (
	// RatingBuy indicates a strong buy recommendation.
	RatingBuy PortfolioRating = "Buy"
	// RatingOverweight indicates overweight position recommended.
	RatingOverweight PortfolioRating = "Overweight"
	// RatingHold indicates hold current position.
	RatingHold PortfolioRating = "Hold"
	// RatingUnderweight indicates underweight position recommended.
	RatingUnderweight PortfolioRating = "Underweight"
	// RatingSell indicates sell recommendation.
	RatingSell PortfolioRating = "Sell"
)

// validRatings is the set of all accepted portfolio ratings.
var validRatings = map[PortfolioRating]bool{
	RatingBuy:         true,
	RatingOverweight:  true,
	RatingHold:        true,
	RatingUnderweight: true,
	RatingSell:        true,
}

// IsValid returns true if the rating is one of the five defined values.
func (r PortfolioRating) IsValid() bool {
	return validRatings[r]
}

// String returns the string representation of the rating.
func (r PortfolioRating) String() string {
	return string(r)
}

// ParsePortfolioRating parses a string into a PortfolioRating.
// Returns an error if the string does not match any defined rating.
func ParsePortfolioRating(s string) (PortfolioRating, error) {
	r := PortfolioRating(s)
	if !r.IsValid() {
		return "", fmt.Errorf("invalid portfolio rating %q: must be one of Buy, Overweight, Hold, Underweight, Sell", s)
	}
	return r, nil
}

// ─── ResearchPlan ──────────────────────────────────────────

// ResearchPlan is produced by the Research Manager after bull/bear debate convergence.
type ResearchPlan struct {
	Recommendation  PortfolioRating `json:"recommendation"`
	Rationale       string          `json:"rationale"`
	StrategicAction string          `json:"strategic_action"`
}

// Validate checks that ResearchPlan has required fields populated.
func (p *ResearchPlan) Validate() error {
	if p == nil {
		return fmt.Errorf("research plan is nil")
	}
	if !p.Recommendation.IsValid() {
		return fmt.Errorf("invalid recommendation: %q", p.Recommendation)
	}
	if strings.TrimSpace(p.Rationale) == "" {
		return fmt.Errorf("rationale must not be empty")
	}
	if strings.TrimSpace(p.StrategicAction) == "" {
		return fmt.Errorf("strategic action must not be empty")
	}
	return nil
}

// ─── TraderProposal ────────────────────────────────────────

// TraderProposal is produced by the Trader agent based on the research plan.
type TraderProposal struct {
	Action         string   `json:"action"`
	Reasoning      string   `json:"reasoning"`
	EntryPrice     *float64 `json:"entry_price,omitempty"`
	StopLoss       *float64 `json:"stop_loss,omitempty"`
	PositionSizing string   `json:"position_sizing,omitempty"`
}

// Validate checks that TraderProposal has required fields populated.
func (tp *TraderProposal) Validate() error {
	if tp == nil {
		return fmt.Errorf("trader proposal is nil")
	}
	if strings.TrimSpace(tp.Action) == "" {
		return fmt.Errorf("action must not be empty")
	}
	if strings.TrimSpace(tp.Reasoning) == "" {
		return fmt.Errorf("reasoning must not be empty")
	}
	if tp.EntryPrice != nil && *tp.EntryPrice <= 0 {
		return fmt.Errorf("entry price must be positive, got %f", *tp.EntryPrice)
	}
	if tp.StopLoss != nil && *tp.StopLoss <= 0 {
		return fmt.Errorf("stop loss must be positive, got %f", *tp.StopLoss)
	}
	if tp.EntryPrice != nil && tp.StopLoss != nil && *tp.StopLoss >= *tp.EntryPrice {
		return fmt.Errorf("stop loss (%f) must be below entry price (%f)", *tp.StopLoss, *tp.EntryPrice)
	}
	return nil
}

// ─── PortfolioDecision ─────────────────────────────────────

// PortfolioDecision is the final output from the Portfolio Manager after risk debate.
type PortfolioDecision struct {
	Rating           PortfolioRating `json:"rating"`
	ExecutiveSummary string          `json:"executive_summary"`
	InvestmentThesis string          `json:"investment_thesis"`
	PriceTarget      *float64        `json:"price_target,omitempty"`
	TimeHorizon      string          `json:"time_horizon,omitempty"`
}

// Validate checks that PortfolioDecision has required fields populated.
func (pd *PortfolioDecision) Validate() error {
	if pd == nil {
		return fmt.Errorf("portfolio decision is nil")
	}
	if !pd.Rating.IsValid() {
		return fmt.Errorf("invalid rating: %q", pd.Rating)
	}
	if strings.TrimSpace(pd.ExecutiveSummary) == "" {
		return fmt.Errorf("executive summary must not be empty")
	}
	if strings.TrimSpace(pd.InvestmentThesis) == "" {
		return fmt.Errorf("investment thesis must not be empty")
	}
	if pd.PriceTarget != nil && *pd.PriceTarget <= 0 {
		return fmt.Errorf("price target must be positive, got %f", *pd.PriceTarget)
	}
	return nil
}

// ─── AnalystReport ─────────────────────────────────────────

// AnalystReport captures the structured output from an individual analyst agent.
type AnalystReport struct {
	AnalystName string                 `json:"analyst_name"`
	AnalystType string                 `json:"analyst_type"` // market/sentiment/news/fundamentals/technical
	Score       float64                `json:"score"`        // 0-100
	Verdict     string                 `json:"verdict"`
	Findings    map[string]interface{} `json:"findings"`
	Confidence  float64                `json:"confidence"` // 0-1
	RawOutput   string                 `json:"raw_output"` // original LLM output
	Timestamp   time.Time              `json:"timestamp"`
}

// Validate checks that AnalystReport has required fields within valid ranges.
func (ar *AnalystReport) Validate() error {
	if ar == nil {
		return fmt.Errorf("analyst report is nil")
	}
	if strings.TrimSpace(ar.AnalystName) == "" {
		return fmt.Errorf("analyst name must not be empty")
	}
	validTypes := map[string]bool{
		"market": true, "sentiment": true, "news": true,
		"fundamentals": true, "technical": true,
	}
	if !validTypes[ar.AnalystType] {
		return fmt.Errorf("invalid analyst type %q: must be market/sentiment/news/fundamentals/technical", ar.AnalystType)
	}
	if ar.Score < 0 || ar.Score > 100 {
		return fmt.Errorf("score must be in [0,100], got %f", ar.Score)
	}
	validVerdicts := map[string]bool{
		"strong_buy": true, "buy": true, "overweight": true,
		"hold": true, "underweight": true, "sell": true,
		"strong_sell": true, "neutral": true, "avoid": true,
		"bullish": true, "bearish": true,
	}
	if strings.TrimSpace(ar.Verdict) == "" {
		return fmt.Errorf("verdict must not be empty")
	}
	if !validVerdicts[strings.ToLower(strings.TrimSpace(ar.Verdict))] {
		return fmt.Errorf("invalid verdict %q: must be one of strong_buy/buy/overweight/hold/underweight/sell/strong_sell/neutral/avoid/bullish/bearish", ar.Verdict)
	}
	if ar.Confidence < 0 || ar.Confidence > 1 {
		return fmt.Errorf("confidence must be in [0,1], got %f", ar.Confidence)
	}
	return nil
}

// ─── SentimentReport ───────────────────────────────────────

// SentimentReport captures sentiment analysis results with band classification.
type SentimentReport struct {
	Band       string   `json:"band"`       // strongly_bullish/bullish/neutral/bearish/strongly_bearish
	Score      float64  `json:"score"`      // 0-1
	Confidence float64  `json:"confidence"` // 0-1
	Signals    []string `json:"signals"`
}

// ValidBands defines all acceptable sentiment bands.
var ValidBands = map[string]bool{
	"strongly_bullish": true,
	"bullish":          true,
	"neutral":          true,
	"bearish":          true,
	"strongly_bearish": true,
}

// Validate checks that SentimentReport has valid band and score ranges.
func (sr *SentimentReport) Validate() error {
	if sr == nil {
		return fmt.Errorf("sentiment report is nil")
	}
	if !ValidBands[sr.Band] {
		return fmt.Errorf("invalid sentiment band %q", sr.Band)
	}
	if sr.Score < 0 || sr.Score > 1 {
		return fmt.Errorf("sentiment score must be in [0,1], got %f", sr.Score)
	}
	if sr.Confidence < 0 || sr.Confidence > 1 {
		return fmt.Errorf("sentiment confidence must be in [0,1], got %f", sr.Confidence)
	}
	return nil
}

// ─── VerifiedMarketSnapshot ────────────────────────────────

// Candle is an alias for market.Candle to avoid circular imports in schema types.
type Candle = market.Candle

// VerifiedMarketSnapshot provides a deterministic, validated view of market data
// for a specific analysis date. It constrains analysts to use only verified data.
type VerifiedMarketSnapshot struct {
	RequestedDate time.Time          `json:"requested_date"`
	LatestRowDate time.Time          `json:"latest_row_date"`
	OHLCV         *Candle            `json:"ohlcv"`
	Indicators    map[string]float64 `json:"indicators"`
	RecentCloses  []float64          `json:"recent_closes"`
	Warning       string             `json:"warning"`
}

// Validate checks that VerifiedMarketSnapshot contains usable data.
func (vs *VerifiedMarketSnapshot) Validate() error {
	if vs == nil {
		return fmt.Errorf("market snapshot is nil")
	}
	if vs.OHLCV == nil {
		return fmt.Errorf("OHLCV data is missing")
	}
	if len(vs.RecentCloses) == 0 {
		return fmt.Errorf("recent closes are empty")
	}
	return nil
}
