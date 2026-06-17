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
	b.WriteString(fmt.Sprintf("- **Recommendation:** %s\n", rp.Recommendation))
	b.WriteString(fmt.Sprintf("- **Rationale:** %s\n", rp.Rationale))
	b.WriteString(fmt.Sprintf("- **Strategic Action:** %s\n", rp.StrategicAction))
	return b.String()
}

// RenderMarkdownTP renders a TraderProposal to stable markdown format.
func RenderMarkdownTP(tp *TraderProposal) string {
	if tp == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Trader Proposal\n\n")
	b.WriteString(fmt.Sprintf("- **Action:** %s\n", tp.Action))
	b.WriteString(fmt.Sprintf("- **Reasoning:** %s\n", tp.Reasoning))
	if tp.EntryPrice != nil {
		b.WriteString(fmt.Sprintf("- **Entry Price:** %.2f\n", *tp.EntryPrice))
	}
	if tp.StopLoss != nil {
		b.WriteString(fmt.Sprintf("- **Stop Loss:** %.2f\n", *tp.StopLoss))
	}
	if tp.PositionSizing != "" {
		b.WriteString(fmt.Sprintf("- **Position Sizing:** %s\n", tp.PositionSizing))
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
	b.WriteString(fmt.Sprintf("- **Rating:** %s\n", pd.Rating))
	b.WriteString(fmt.Sprintf("- **Executive Summary:** %s\n", pd.ExecutiveSummary))
	b.WriteString(fmt.Sprintf("- **Investment Thesis:** %s\n", pd.InvestmentThesis))
	if pd.PriceTarget != nil {
		b.WriteString(fmt.Sprintf("- **Price Target:** %.2f\n", *pd.PriceTarget))
	}
	if pd.TimeHorizon != "" {
		b.WriteString(fmt.Sprintf("- **Time Horizon:** %s\n", pd.TimeHorizon))
	}
	return b.String()
}

// RenderMarkdownAR renders an AnalystReport to stable markdown format.
func RenderMarkdownAR(ar *AnalystReport) string {
	if ar == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Analyst Report: %s\n\n", ar.AnalystName))
	b.WriteString(fmt.Sprintf("- **Type:** %s\n", ar.AnalystType))
	b.WriteString(fmt.Sprintf("- **Score:** %.1f/100\n", ar.Score))
	b.WriteString(fmt.Sprintf("- **Verdict:** %s\n", ar.Verdict))
	b.WriteString(fmt.Sprintf("- **Confidence:** %.2f\n", ar.Confidence))
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
