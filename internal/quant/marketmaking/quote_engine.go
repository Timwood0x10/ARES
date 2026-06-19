package marketmaking

import (
	"context"
	"fmt"
	"math"
	"time"
)

// QuoteEngine produces two-sided quotes based on mid price, spread, inventory, and risk.
type QuoteEngine struct {
	BaseSpread     float64       // half-spread as fraction of mid price (e.g., 0.001)
	SkewFactor     float64       // inventory skew sensitivity (e.g., 0.1)
	MaxInventory   float64       // max absolute inventory value before stopping
	RiskLimit      float64       // risk utilization threshold (0-1) for quoting
	MaxQuoteSize   float64       // maximum quote size
	StaleThreshold time.Duration // max age of data before refusing to quote
}

// QuoteEngineConfig holds configuration for the quote engine.
type QuoteEngineConfig struct {
	BaseSpread     float64
	SkewFactor     float64
	MaxInventory   float64
	RiskLimit      float64
	MaxQuoteSize   float64
	StaleThreshold time.Duration
}

// NewQuoteEngine creates a new QuoteEngine from the given configuration.
//
// Args:
//
//	cfg: configuration parameters for the quote engine.
//
// Returns:
//
//	a configured *QuoteEngine, or an error if any config value is invalid.
func NewQuoteEngine(cfg *QuoteEngineConfig) (*QuoteEngine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("quote engine config is nil")
	}
	if cfg.BaseSpread <= 0 {
		return nil, fmt.Errorf("base spread must be > 0, got %f", cfg.BaseSpread)
	}
	if cfg.MaxQuoteSize <= 0 {
		return nil, fmt.Errorf("max quote size must be > 0, got %f", cfg.MaxQuoteSize)
	}
	if cfg.RiskLimit <= 0 || cfg.RiskLimit > 1 {
		return nil, fmt.Errorf("risk limit must be in (0, 1], got %f", cfg.RiskLimit)
	}
	if cfg.MaxInventory <= 0 {
		return nil, fmt.Errorf("max inventory must be > 0, got %f", cfg.MaxInventory)
	}
	if cfg.StaleThreshold <= 0 {
		return nil, fmt.Errorf("stale threshold must be > 0, got %v", cfg.StaleThreshold)
	}

	return &QuoteEngine{
		BaseSpread:     cfg.BaseSpread,
		SkewFactor:     cfg.SkewFactor,
		MaxInventory:   cfg.MaxInventory,
		RiskLimit:      cfg.RiskLimit,
		MaxQuoteSize:   cfg.MaxQuoteSize,
		StaleThreshold: cfg.StaleThreshold,
	}, nil
}

// GenerateQuote produces a bid/ask quote decision based on current market data,
// inventory state, and risk assessment.
//
// Args:
//
//	ctx: context for cancellation.
//	event: current market data tick.
//	inv: current inventory state.
//	risk: current risk snapshot.
//
// Returns:
//
//	a *Quote ready to submit, or an error if quoting should be withheld.
func (e *QuoteEngine) GenerateQuote(ctx context.Context, event *MarketDataEvent, inv *Inventory, risk *RiskSnapshot) (*Quote, error) {
	// 1. Input validation
	if event == nil {
		return nil, fmt.Errorf("market data event is nil")
	}
	if event.MidPrice <= 0 {
		return nil, fmt.Errorf("mid price must be > 0, got %f", event.MidPrice)
	}
	if event.IsStaleData(e.StaleThreshold) {
		return nil, fmt.Errorf("market data is stale, refusing to quote")
	}

	// 2. Risk check
	if risk != nil && (risk.IsCritical || risk.Utilization > e.RiskLimit) {
		return nil, fmt.Errorf("risk check failed: utilization=%.2f, critical=%v, refusing to quote",
			risk.Utilization, risk.IsCritical)
	}

	mid := event.MidPrice

	// 3. Calculate inventory skew
	var netInvValue float64
	if inv != nil {
		netInvValue = inv.NetInventoryValue()
	}
	skew := e.SkewFactor * (netInvValue / e.MaxInventory)

	// Clamp skew to reasonable range to prevent extreme quotes
	skew = math.Max(-0.9, math.Min(0.9, skew))

	// 4. Calculate quoted prices
	halfSpread := e.BaseSpread * mid
	bid := mid - halfSpread*(1+skew)
	ask := mid + halfSpread*(1-skew)

	// Ensure bid < ask after skew adjustments
	if bid >= ask {
		bid = mid - halfSpread
		ask = mid + halfSpread
	}

	// Ensure positive prices
	if bid <= 0 || ask <= 0 {
		return nil, fmt.Errorf("calculated non-positive bid (%f) or ask (%f)", bid, ask)
	}

	// 5. Determine quote size — reduce if inventory is approaching limits
	quoteSize := e.MaxQuoteSize
	if inv != nil && e.MaxInventory > 0 {
		absNetInv := math.Abs(netInvValue)
		if absNetInv > e.MaxInventory*0.8 {
			// Scale down linearly as we approach the limit
			scaleFactor := (e.MaxInventory - absNetInv) / (e.MaxInventory * 0.2)
			scaleFactor = math.Max(0, scaleFactor)
			quoteSize = e.MaxQuoteSize * scaleFactor
		}
		// If at or beyond limit, stop quoting entirely
		if absNetInv >= e.MaxInventory {
			return nil, fmt.Errorf("inventory limit breached: |%.2f| >= %.2f, refusing to quote",
				netInvValue, e.MaxInventory)
		}
	}

	// Minimum size floor to avoid degenerate quotes
	if quoteSize < 0.001 {
		return nil, fmt.Errorf("quote size too small due to inventory proximity, refusing to quote")
	}

	// 6. Build the quote
	ttl := e.StaleThreshold
	if ttl <= 0 {
		ttl = 5 * time.Second
	}

	q := &Quote{
		Symbol:    event.Symbol,
		BidPrice:  bid,
		AskPrice:  ask,
		BidSize:   quoteSize,
		AskSize:   quoteSize,
		Timestamp: time.Now(),
		TTL:       ttl,
		Status:    QuoteLive,
	}

	// 7. Validate the quote
	if err := validateQuote(q); err != nil {
		return nil, fmt.Errorf("generated quote failed validation: %w", err)
	}

	return q, nil
}

// validateQuote checks that a Quote has valid fields.
func validateQuote(q *Quote) error {
	if q == nil {
		return fmt.Errorf("quote is nil")
	}
	if q.Symbol == "" {
		return fmt.Errorf("symbol must not be empty")
	}
	if q.BidPrice <= 0 || q.AskPrice <= 0 {
		return fmt.Errorf("bid/ask price must be > 0")
	}
	if q.BidPrice >= q.AskPrice {
		return fmt.Errorf("bid price (%f) must be < ask price (%f)", q.BidPrice, q.AskPrice)
	}
	if q.BidSize <= 0 || q.AskSize <= 0 {
		return fmt.Errorf("bid/ask size must be > 0")
	}
	if q.TTL <= 0 {
		return fmt.Errorf("ttl must be > 0")
	}
	return nil
}
