package portfolio

import (
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/quant/research"
)

// ValidateTradeSignal checks that a TradeSignal has valid fields.
//
// Args:
//   - sig: the trade signal to validate.
//
// Returns:
//   - error if any field is invalid.
func ValidateTradeSignal(sig TradeSignal) error {
	if sig.Date.IsZero() {
		return fmt.Errorf("trade signal date is zero")
	}
	switch sig.Action {
	case "BUY", "SELL", "HOLD":
		// Valid actions.
	default:
		return fmt.Errorf("invalid trade signal action %q: must be BUY, SELL, or HOLD", sig.Action)
	}
	if sig.Confidence < 0 || sig.Confidence > 1 {
		return fmt.Errorf("confidence must be in [0,1], got %f", sig.Confidence)
	}
	return nil
}

// GenerateSignalsFromResearch converts a research PortfolioDecision into
// a slice of TradeSignals. It creates:
//   - A BUY signal when rating is Buy or Overweight.
//   - A SELL signal when rating is Underweight or Sell.
//   - A HOLD signal otherwise (Hold rating).
//
// The signal date is set to time.Now(); callers should adjust if they need
// a specific simulation date.
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
