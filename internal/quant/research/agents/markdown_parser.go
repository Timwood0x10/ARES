package agents

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"goagentx/internal/quant/research"
)

// MarkdownParser extracts structured data from markdown-rendered research outputs.
// It is used when the LLM produces human-readable markdown instead of raw JSON.
type MarkdownParser struct{}

// NewMarkdownParser creates a new MarkdownParser instance.
//
// Returns:
//   - pointer to the initialized parser.
func NewMarkdownParser() *MarkdownParser {
	return &MarkdownParser{}
}

// ParseRating extracts a PortfolioRating from markdown text.
// It looks for rating keywords in the text and maps them to PortfolioRating values.
//
// FIX: uses ordered slice with word-boundary matching instead of map iteration,
// ensuring longer names (e.g., "overweight") match before shorter substrings
// and providing deterministic matching order across runs.
//
// Args:
//   - md: the markdown text to search.
//
// Returns:
//   - the extracted PortfolioRating, defaults to RatingHold if not found.
//   - nil error always (fallback to Hold is acceptable).
func (m *MarkdownParser) ParseRating(md string) (research.PortfolioRating, error) {
	mdLower := strings.ToLower(md)
	// Ordered by longest name first to prevent "buy" inside "overweight" from matching first.
	ratingPatterns := []struct {
		pattern string
		rating  research.PortfolioRating
	}{
		{`\boverweight\b`, research.RatingOverweight},
		{`\bunderweight\b`, research.RatingUnderweight},
		{`\bbuy\b`, research.RatingBuy},
		{`\bhold\b`, research.RatingHold},
		{`\bsell\b`, research.RatingSell},
	}
	for _, rp := range ratingPatterns {
		matched, err := regexp.MatchString(rp.pattern, mdLower)
		if err != nil || !matched {
			continue
		}
		return rp.rating, nil
	}
	return research.RatingHold, nil
}

// ParseScore extracts a numeric score from markdown text.
// It looks for patterns like "score: 7.5" or "Score: 8/10".
//
// Args:
//   - md: the markdown text to search.
//
// Returns:
//   - the extracted score as float64.
//   - error if no score pattern is found.
func (m *MarkdownParser) ParseScore(md string) (float64, error) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)score[:\s]+(\d+(?:\.\d+)?)`),
		regexp.MustCompile(`(?i)rating[:\s]+(\d+(?:\.\d+)?)`),
		regexp.MustCompile(`(\d+(?:\.\d+)?)\s*/\s*10`),
	}
	for _, re := range patterns {
		matches := re.FindStringSubmatch(md)
		if len(matches) >= 2 {
			score, err := strconv.ParseFloat(matches[1], 64)
			if err == nil {
				return score, nil
			}
		}
	}
	return 0, fmt.Errorf("no score found in markdown text")
}

// ParseResearchPlan extracts a ResearchPlan from markdown text.
// It looks for structured sections in the markdown.
//
// Args:
//   - md: the markdown text to parse.
//
// Returns:
//   - the extracted ResearchPlan.
//   - nil error always (defaults used for missing fields).
func (m *MarkdownParser) ParseResearchPlan(md string) (*research.ResearchPlan, error) {
	rating, _ := m.ParseRating(md)
	rationale := m.extractSection(md, "rationale", "reasoning", "analysis")
	action := m.extractSection(md, "action", "recommendation", "strategic")
	return &research.ResearchPlan{
		Recommendation:  rating,
		Rationale:       rationale,
		StrategicAction: action,
	}, nil
}

// ParseTraderProposal extracts a TraderProposal from markdown text.
//
// Args:
//   - md: the markdown text to parse.
//
// Returns:
//   - the extracted TraderProposal.
//   - nil error always (defaults used for missing fields).
func (m *MarkdownParser) ParseTraderProposal(md string) (*research.TraderProposal, error) {
	action := m.extractSection(md, "action", "signal", "decision")
	reasoning := m.extractSection(md, "reasoning", "rationale", "thesis")
	entryPrice := parseFloatField(md, "entry_price", "entry price", "price")
	stopLoss := parseFloatField(md, "stop_loss", "stop loss")
	positionSizing := m.extractSection(md, "position_sizing", "position size", "sizing")
	return &research.TraderProposal{
		Action:         action,
		Reasoning:      reasoning,
		EntryPrice:     entryPrice,
		StopLoss:       stopLoss,
		PositionSizing: positionSizing,
	}, nil
}

// ParsePortfolioDecision extracts a PortfolioDecision from markdown text.
//
// Args:
//   - md: the markdown text to parse.
//
// Returns:
//   - the extracted PortfolioDecision.
//   - nil error always (defaults used for missing fields).
func (m *MarkdownParser) ParsePortfolioDecision(md string) (*research.PortfolioDecision, error) {
	rating, _ := m.ParseRating(md)
	summary := m.extractSection(md, "summary", "executive summary", "conclusion")
	thesis := m.extractSection(md, "thesis", "investment thesis", "rationale")
	priceTarget := parseFloatField(md, "price_target", "price target", "target")
	timeHorizon := m.extractSection(md, "time_horizon", "time horizon", "horizon")
	return &research.PortfolioDecision{
		Rating:           rating,
		ExecutiveSummary: summary,
		InvestmentThesis: thesis,
		PriceTarget:      priceTarget,
		TimeHorizon:      timeHorizon,
	}, nil
}

// ParseFromJSONBlock extracts JSON from a markdown code block and unmarshals it.
// This is useful when LLM wraps JSON output in markdown formatting.
//
// Args:
//   - md: the markdown text containing a JSON code block.
//   - target: the target struct to unmarshal into.
//
// Returns:
//   - error if JSON extraction or unmarshaling fails.
func (m *MarkdownParser) ParseFromJSONBlock(md string, target interface{}) error {
	backtick := "`"
	jsonBlockPattern := "(?s)(?:" + backtick + "json\\s*\\n?)(.*?)(?:\\n?\\s*" + backtick + backtick + backtick + ")"
	re := regexp.MustCompile(jsonBlockPattern)
	matches := re.FindStringSubmatch(md)
	if len(matches) < 2 {
		return fmt.Errorf("no JSON code block found in markdown")
	}
	return json.Unmarshal([]byte(matches[1]), target)
}

// ─── Internal helpers ──────────────────────────────────

func (m *MarkdownParser) extractSection(md string, keys ...string) string {
	for _, key := range keys {
		pattern := regexp.MustCompile(
			fmt.Sprintf("(?i)(?:^|\\n)[#\\s]*%s[:\\s]*\\n?(.*?)(?:\\n(?:#|\\*)|$)",
				regexp.QuoteMeta(key)),
		)
		matches := pattern.FindStringSubmatch(md)
		if len(matches) >= 2 {
			text := strings.TrimSpace(matches[1])
			if idx := strings.Index(text, "\n\n"); idx > 0 {
				text = text[:idx]
			}
			return text
		}
	}
	return ""
}

func parseFloatField(md string, keys ...string) *float64 {
	for _, key := range keys {
		pattern := regexp.MustCompile(
			fmt.Sprintf("(?i)%s[:\\s]*(\\d+(?:\\.\\d+)?)",
				regexp.QuoteMeta(key)),
		)
		matches := pattern.FindStringSubmatch(md)
		if len(matches) >= 2 {
			val, err := strconv.ParseFloat(matches[1], 64)
			if err == nil {
				return &val
			}
		}
	}
	return nil
}
