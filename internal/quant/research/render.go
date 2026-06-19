package research

import (
	"fmt"
	"strings"
)

// RenderMarkdown renders a ResearchPlan to stable markdown format.
func RenderMarkdown(rp *ResearchPlan) string {
	if rp == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Research Plan\n\n")
	fmt.Fprintf(&b, "- **Recommendation:** %s\n", rp.Recommendation)
	fmt.Fprintf(&b, "- **Rationale:** %s\n", rp.Rationale)
	fmt.Fprintf(&b, "- **Strategic Action:** %s\n", rp.StrategicAction)
	return b.String()
}

// RenderMarkdownTP renders a TraderProposal to stable markdown format.
func RenderMarkdownTP(tp *TraderProposal) string {
	if tp == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Trader Proposal\n\n")
	fmt.Fprintf(&b, "- **Action:** %s\n", tp.Action)
	fmt.Fprintf(&b, "- **Reasoning:** %s\n", tp.Reasoning)
	if tp.EntryPrice != nil {
		fmt.Fprintf(&b, "- **Entry Price:** %.2f\n", *tp.EntryPrice)
	}
	if tp.StopLoss != nil {
		fmt.Fprintf(&b, "- **Stop Loss:** %.2f\n", *tp.StopLoss)
	}
	if tp.PositionSizing != "" {
		fmt.Fprintf(&b, "- **Position Sizing:** %s\n", tp.PositionSizing)
	}
	return b.String()
}

// RenderMarkdownPD renders a PortfolioDecision to stable markdown format.
func RenderMarkdownPD(pd *PortfolioDecision) string {
	if pd == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Portfolio Decision\n\n")
	fmt.Fprintf(&b, "- **Rating:** %s\n", pd.Rating)
	fmt.Fprintf(&b, "- **Executive Summary:** %s\n", pd.ExecutiveSummary)
	fmt.Fprintf(&b, "- **Investment Thesis:** %s\n", pd.InvestmentThesis)
	if pd.PriceTarget != nil {
		fmt.Fprintf(&b, "- **Price Target:** %.2f\n", *pd.PriceTarget)
	}
	if pd.TimeHorizon != "" {
		fmt.Fprintf(&b, "- **Time Horizon:** %s\n", pd.TimeHorizon)
	}
	return b.String()
}

// RenderMarkdownAR renders an AnalystReport to stable markdown format.
func RenderMarkdownAR(ar *AnalystReport) string {
	if ar == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Analyst Report: %s\n\n", ar.AnalystName)
	fmt.Fprintf(&b, "- **Type:** %s\n", ar.AnalystType)
	fmt.Fprintf(&b, "- **Score:** %.1f/100\n", ar.Score)
	fmt.Fprintf(&b, "- **Verdict:** %s\n", ar.Verdict)
	fmt.Fprintf(&b, "- **Confidence:** %.2f\n", ar.Confidence)
	return b.String()
}

// ParseRatingFromMarkdown extracts a PortfolioRating from markdown text.
// It looks for the pattern "- **Rating:** <value>" in the markdown content.
func ParseRatingFromMarkdown(md string) (PortfolioRating, error) {
	if md == "" {
		return "", fmt.Errorf("empty markdown input")
	}
	const prefix = "- **Rating:** "
	idx := strings.Index(md, prefix)
	if idx == -1 {
		return "", fmt.Errorf("rating header not found in markdown")
	}
	start := idx + len(prefix)
	end := strings.IndexByte(md[start:], '\n')
	if end == -1 {
		end = len(md[start:])
	} else {
		end = start + end
	}
	value := strings.TrimSpace(md[start:end])
	return ParsePortfolioRating(value)
}
