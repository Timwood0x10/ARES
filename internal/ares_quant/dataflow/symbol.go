package dataflow

import (
	"fmt"
	"strings"
)

// NormalizationRule defines a single symbol transformation rule.
type NormalizationRule struct {
	Pattern     string // prefix or exact match pattern
	Replacement string // normalized output
	AssetClass  string // stock/crypto/forex/commodity/index
}

// Normalizer standardizes symbol formats across different data sources.
// It applies rules in registration order; first match wins.
type Normalizer struct {
	rules []NormalizationRule
}

// NewNormalizer creates a Normalizer with built-in common normalization rules.
func NewNormalizer() *Normalizer {
	return &Normalizer{
		rules: []NormalizationRule{
			// Crypto: exchange-specific -> standard format
			{Pattern: "BTCUSDT", Replacement: "BTC-USD", AssetClass: "crypto"},
			{Pattern: "ETHUSDT", Replacement: "ETH-USD", AssetClass: "crypto"},
			// Commodity: common aliases -> standard futures format
			{Pattern: "XAUUSD", Replacement: "GC=F", AssetClass: "commodity"},
			{Pattern: "XAGUSD", Replacement: "SI=F", AssetClass: "commodity"},
			// Forex: raw pair -> Yahoo-style suffix
			{Pattern: "EURUSD", Replacement: "EURUSD=X", AssetClass: "forex"},
			{Pattern: "GBPUSD", Replacement: "GBPUSD=X", AssetClass: "forex"},
			{Pattern: "USDJPY", Replacement: "USDJPY=X", AssetClass: "forex"},
			// Index CFD: common aliases -> Yahoo index format
			{Pattern: "SPX500", Replacement: "^GSPC", AssetClass: "index"},
			{Pattern: "US100", Replacement: "^IXIC", AssetClass: "index"},
			{Pattern: "US30", Replacement: "^DJI", AssetClass: "index"},
			// Stocks: pass through unchanged (already in correct format)
			// AAPL, 0700.HK, 600519.SS etc. are handled by the default path
		},
	}
}

// Normalize transforms a symbol into its canonical form using registered rules.
// If no rule matches, the original symbol is returned as-is (assumed already normalized).
// Returns ErrSymbolEmpty if the input is empty after trimming.
func (n *Normalizer) Normalize(symbol string) (string, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return "", fmt.Errorf("%w: input is empty or whitespace", ErrSymbolEmpty)
	}
	for _, rule := range n.rules {
		if strings.HasPrefix(symbol, rule.Pattern) || symbol == rule.Pattern {
			// For exact match, full replacement; for prefix match, replace prefix part.
			if symbol == rule.Pattern {
				return rule.Replacement, nil
			}
			replaced := rule.Replacement + strings.TrimPrefix(symbol, rule.Pattern)
			return replaced, nil
		}
	}
	// No matching rule — assume symbol is already in canonical form.
	return symbol, nil
}

// DetectAssetClass returns the asset class for a symbol based on normalization rules.
// Returns "unknown" if no rule matches and heuristics cannot determine the class.
func (n *Normalizer) DetectAssetClass(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	for _, rule := range n.rules {
		if strings.HasPrefix(symbol, rule.Pattern) || symbol == rule.Pattern {
			return rule.AssetClass
		}
	}
	// Heuristic detection for symbols that passed through without a rule match.
	switch {
	case strings.Contains(symbol, ".HK") || strings.Contains(symbol, ".SS") || strings.Contains(symbol, ".SZ"):
		return "stock"
	case strings.HasSuffix(symbol, "=X"):
		return "forex"
	case strings.HasPrefix(symbol, "^"):
		return "index"
	case len(symbol) <= 5 && !strings.Contains(symbol, "-") && !strings.Contains(symbol, "="):
		return "stock"
	default:
		return "unknown"
	}
}
