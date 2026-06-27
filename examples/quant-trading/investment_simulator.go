// Package main — signal generation bridge between research layer and simulation.
// This file converts research PortfolioDecision into trade signal objects.
// It does NOT depend on internal/ares_quant/portfolio for simulation — that is
// handled by main.go via the public marketmaking API.
package main

import (
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_quant/research"
)

// TradeSignal represents a time-based trading instruction produced by the
// research layer. It mirrors the shape of marketmaking.TradeSignal but lives
// here to avoid importing api/marketmaking (which would create a cycle through
// internal/ares_quant/marketmaking).
type TradeSignal struct {
	Date       time.Time `json:"date"`
	Action     string    `json:"action"` // "BUY", "SELL", or "HOLD"
	Reason     string    `json:"reason,omitempty"`
	Confidence float64   `json:"confidence,omitempty"` // 0–1
}

// GenerateSignalsFromResearch converts a research PortfolioDecision into
// a slice of TradeSignals. It creates:
//   - A BUY signal when rating is Buy or Overweight.
//   - A SELL signal when rating is Underweight or Sell.
//   - A HOLD signal otherwise (Hold rating).
//
// Args:
//   - decision: the portfolio decision from the research layer.
//
// Returns:
//   - slice containing one TradeSignal, or a HOLD signal with explanation
//     if the decision is nil or has an empty rating.
func GenerateSignalsFromResearch(decision *research.PortfolioDecision) []TradeSignal {
	if decision == nil {
		return []TradeSignal{
			{Date: time.Now(), Action: "HOLD", Reason: "no decision available"},
		}
	}
	if decision.Rating == "" {
		return []TradeSignal{
			{Date: time.Now(), Action: "HOLD", Reason: "invalid decision: empty rating"},
		}
	}

	action := "HOLD"
	reason := fmt.Sprintf("Rating=%s: %s", decision.Rating, decision.ExecutiveSummary)

	switch decision.Rating {
	case research.RatingBuy, research.RatingOverweight:
		action = "BUY"
	case research.RatingUnderweight, research.RatingSell:
		action = "SELL"
	}

	confidence := 0.7
	if decision.PriceTarget != nil && *decision.PriceTarget > 0 {
		confidence = 0.8
	}

	return []TradeSignal{
		{
			Date:       time.Now(),
			Action:     action,
			Reason:     reason,
			Confidence: confidence,
		},
	}
}
