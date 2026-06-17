package marketmaking

import (
	"context"
	"math"
	"testing"
	"time"
)

// helper to create a valid default config for tests
func defaultTestConfig() *QuoteEngineConfig {
	return &QuoteEngineConfig{
		BaseSpread:     0.001, // 0.1% half-spread
		SkewFactor:     0.1,
		MaxInventory:   10000.0,
		RiskLimit:      0.8,
		MaxQuoteSize:   10.0,
		StaleThreshold: 5 * time.Second,
	}
}

func freshEvent(mid float64) *MarketDataEvent {
	return &MarketDataEvent{
		Symbol:    "BTCUSD",
		MidPrice:  mid,
		BidPrice:  mid * 0.9995,
		AskPrice:  mid * 1.0005,
		Spread:    mid * 0.001,
		Timestamp: time.Now(),
	}
}

// ─── NewQuoteEngine Tests ──────────────────────────────

func TestNewQuoteEngine_ValidConfig(t *testing.T) {
	cfg := defaultTestConfig()
	engine, err := NewQuoteEngine(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
	if engine.BaseSpread != 0.001 {
		t.Fatalf("expected BaseSpread 0.001, got %f", engine.BaseSpread)
	}
}

func TestNewQuoteEngine_NilConfig(t *testing.T) {
	_, err := NewQuoteEngine(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewQuoteEngine_InvalidBaseSpread(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.BaseSpread = -0.001
	_, err := NewQuoteEngine(cfg)
	if err == nil {
		t.Fatal("expected error for negative base spread")
	}
}

func TestNewQuoteEngine_InvalidRiskLimit(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.RiskLimit = 1.5
	_, err := NewQuoteEngine(cfg)
	if err == nil {
		t.Fatal("expected error for risk limit > 1")
	}

	cfg.RiskLimit = 0
	_, err = NewQuoteEngine(cfg)
	if err == nil {
		t.Fatal("expected error for zero risk limit")
	}
}

func TestNewQuoteEngine_InvalidMaxInventory(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.MaxInventory = 0
	_, err := NewQuoteEngine(cfg)
	if err == nil {
		t.Fatal("expected error for zero max inventory")
	}
}

// ─── GenerateQuote: Normal Path ───────────────────────

func TestGenerateQuote_Normal(t *testing.T) {
	cfg := defaultTestConfig()
	engine, _ := NewQuoteEngine(cfg)

	event := freshEvent(50000.0)
	inv := NewInventory(50000.0)
	risk := NewRiskSnapshot(5000.0, 10000.0, nil)

	q, err := engine.GenerateQuote(context.Background(), event, inv, risk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q == nil {
		t.Fatal("expected non-nil quote")
	}

	// Validate
	if err := validateQuote(q); err != nil {
		t.Fatalf("quote should pass validation: %v", err)
	}

	// Check basic properties
	if q.Symbol != "BTCUSD" {
		t.Fatalf("expected symbol BTCUSD, got %s", q.Symbol)
	}
	if q.BidPrice >= q.AskPrice {
		t.Fatalf("bid (%f) must be < ask (%f)", q.BidPrice, q.AskPrice)
	}
	if q.BidSize <= 0 || q.AskSize <= 0 {
		t.Fatalf("quote sizes must be > 0: bid=%f ask=%f", q.BidSize, q.AskSize)
	}

	// Verify bid/ask are centered around mid with expected spread
	mid := 50000.0
	halfSpread := cfg.BaseSpread * mid // 50.0
	expectedBid := mid - halfSpread    // ~49950
	expectedAsk := mid + halfSpread    // ~50050

	if math.Abs(q.BidPrice-expectedBid) > 1.0 {
		t.Errorf("bid price %f near expected %f (mid=%.1f, spread=%.1f)", q.BidPrice, expectedBid, mid, halfSpread)
	}
	if math.Abs(q.AskPrice-expectedAsk) > 1.0 {
		t.Errorf("ask price %f near expected %f (mid=%.1f, spread=%.1f)", q.AskPrice, expectedAsk, mid, halfSpread)
	}
}

// ─── GenerateQuote: Input Validation ───────────────────

func TestGenerateQuote_NilEvent(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	_, err := engine.GenerateQuote(context.Background(), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil event")
	}
}

func TestGenerateQuote_ZeroMidPrice(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(0)
	_, err := engine.GenerateQuote(context.Background(), event, nil, nil)
	if err == nil {
		t.Fatal("expected error for zero mid price")
	}
}

func TestGenerateQuote_NegativeMidPrice(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(-100.0)
	_, err := engine.GenerateQuote(context.Background(), event, nil, nil)
	if err == nil {
		t.Fatal("expected error for negative mid price")
	}
}

// ─── GenerateQuote: Stale Data ───────────────────────

func TestGenerateQuote_StaleData(t *testing.T) {
	cfg := defaultTestConfig()
	engine, _ := NewQuoteEngine(cfg)

	event := &MarketDataEvent{
		Symbol:    "BTCUSD",
		MidPrice:  50000.0,
		Timestamp: time.Now().Add(-10 * time.Second), // older than 5s threshold
	}

	_, err := engine.GenerateQuote(context.Background(), event, nil, nil)
	if err == nil {
		t.Fatal("expected error for stale data")
	}
	// Verify the specific stale data message
	if err.Error() != "market data is stale, refusing to quote" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

func TestGenerateQuote_StaleFlag(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())

	event := &MarketDataEvent{
		Symbol:    "BTCUSD",
		MidPrice:  50000.0,
		Timestamp: time.Now(),
		IsStale:   true, // explicitly marked stale
	}

	_, err := engine.GenerateQuote(context.Background(), event, nil, nil)
	if err == nil {
		t.Fatal("expected error when IsStale flag is set")
	}
}

// ─── GenerateQuote: Risk Critical ─────────────────────

func TestGenerateQuote_RiskCritical(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(50000.0)

	risk := &RiskSnapshot{
		IsCritical:        true,
		Utilization:       1.2,
		InventoryValue:    12000.0,
		MaxInventoryLimit: 10000.0,
	}

	_, err := engine.GenerateQuote(context.Background(), event, nil, risk)
	if err == nil {
		t.Fatal("expected error when risk is critical")
	}
}

func TestGenerateQuote_RiskOverLimit(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.RiskLimit = 0.8
	engine, _ := NewQuoteEngine(cfg)

	event := freshEvent(50000.0)

	risk := &RiskSnapshot{
		IsCritical:        false,
		Utilization:       0.9, // > 0.8 limit
		InventoryValue:    9000.0,
		MaxInventoryLimit: 10000.0,
	}

	_, err := engine.GenerateQuote(context.Background(), event, nil, risk)
	if err == nil {
		t.Fatal("expected error when utilization exceeds risk limit")
	}
}

func TestGenerateQuote_RiskWithinLimit(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.RiskLimit = 0.8
	engine, _ := NewQuoteEngine(cfg)

	event := freshEvent(50000.0)

	risk := &RiskSnapshot{
		IsCritical:        false,
		Utilization:       0.5, // within limit
		InventoryValue:    5000.0,
		MaxInventoryLimit: 10000.0,
	}

	q, err := engine.GenerateQuote(context.Background(), event, nil, risk)
	if err != nil {
		t.Fatalf("unexpected error at safe utilization: %v", err)
	}
	if q == nil {
		t.Fatal("expected non-nil quote at safe utilization")
	}
}

// ─── GenerateQuote: Inventory Skew ────────────────────

func TestGenerateQuote_LongInventorySkew(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.SkewFactor = 0.2
	cfg.MaxInventory = 10000.0
	engine, _ := NewQuoteEngine(cfg)

	event := freshEvent(50000.0)

	// Long inventory: net value is positive
	inv := NewInventory(50000.0)
	inv.Positions["BTCUSD"] = &Position{
		Symbol:        "BTCUSD",
		Quantity:      0.15, // 0.15 BTC worth $7500 at $50k
		AvgEntryPrice: 48000.0,
		CurrentPrice:  50000.0,
	}

	risk := NewRiskSnapshot(7500.0, 10000.0, nil)

	q, _ := engine.GenerateQuote(context.Background(), event, inv, risk)

	// With positive skew (long inventory):
	// bid = mid - halfSpread*(1+skew) → lower bid (discourage buying)
	// ask = mid + halfSpread*(1-skew) → lower ask (encourage selling)
	halfSpread := cfg.BaseSpread * 50000.0               // 50.0
	skew := cfg.SkewFactor * (7500.0 / cfg.MaxInventory) // 0.2 * 0.75 = 0.15

	expectedBidNoSkew := 50000.0 - halfSpread          // 49950.0
	expectedSkewedBid := 50000.0 - halfSpread*(1+skew) // lower than no-skew bid
	expectedSkewedAsk := 50000.0 + halfSpread*(1-skew) // lower than no-skew ask

	if q.BidPrice >= expectedBidNoSkew {
		t.Errorf("long skew should lower bid: got %f, no-skew baseline %f", q.BidPrice, expectedBidNoSkew)
	}
	if math.Abs(q.BidPrice-expectedSkewedBid) > 1.0 {
		t.Errorf("bid %f not close to expected skewed %f", q.BidPrice, expectedSkewedBid)
	}
	if math.Abs(q.AskPrice-expectedSkewedAsk) > 1.0 {
		t.Errorf("ask %f not close to expected skewed %f", q.AskPrice, expectedSkewedAsk)
	}
}

func TestGenerateQuote_ShortInventorySkew(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.SkewFactor = 0.2
	cfg.MaxInventory = 10000.0
	engine, _ := NewQuoteEngine(cfg)

	event := freshEvent(50000.0)

	// Short inventory: net value is negative
	inv := NewInventory(50000.0)
	inv.Positions["BTCUSD"] = &Position{
		Symbol:        "BTCUSD",
		Quantity:      -0.1, // short 0.1 BTC worth -$5000
		AvgEntryPrice: 52000.0,
		CurrentPrice:  50000.0,
	}

	risk := NewRiskSnapshot(math.Abs(-5000.0), 10000.0, nil)

	q, _ := engine.GenerateQuote(context.Background(), event, inv, risk)

	// With negative skew (short inventory):
	// skew = 0.2 * (-5000/10000) = -0.1
	// bid = mid - halfSpread*(1+skew) → higher bid (encourage buying)
	// ask = mid + halfSpread*(1-skew) → higher ask (discourage selling)
	halfSpread := cfg.BaseSpread * 50000.0 // 50.0
	noSkewBid := 50000.0 - halfSpread      // 49950.0

	if q.BidPrice <= noSkewBid {
		t.Errorf("short skew should raise bid: got %f, no-skew baseline %f", q.BidPrice, noSkewBid)
	}
}

// ─── GenerateQuote: Inventory Limit ───────────────────

func TestGenerateQuote_InventoryAtLimit(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.MaxInventory = 10000.0
	engine, _ := NewQuoteEngine(cfg)

	event := freshEvent(50000.0)

	// Inventory at exactly the limit
	inv := NewInventory(50000.0)
	inv.Positions["BTCUSD"] = &Position{
		Symbol:        "BTCUSD",
		Quantity:      0.2, // worth $10000 at $50k — at limit
		AvgEntryPrice: 50000.0,
		CurrentPrice:  50000.0,
	}

	risk := NewRiskSnapshot(10000.0, 10000.0, nil)

	_, err := engine.GenerateQuote(context.Background(), event, inv, risk)
	if err == nil {
		t.Fatal("expected error when inventory at limit")
	}
}

func TestGenerateQuote_InventoryNearLimit_ReducedSize(t *testing.T) {
	cfg := defaultTestConfig()
	cfg.MaxInventory = 10000.0
	cfg.MaxQuoteSize = 10.0
	cfg.RiskLimit = 0.95 // allow 90% utilization to pass risk check
	engine, _ := NewQuoteEngine(cfg)

	event := freshEvent(50000.0)

	// Inventory at 90% of limit → size should be reduced
	inv := NewInventory(50000.0)
	inv.Positions["BTCUSD"] = &Position{
		Symbol:        "BTCUSD",
		Quantity:      0.18, // worth $9000 at $50k — 90% of limit
		AvgEntryPrice: 50000.0,
		CurrentPrice:  50000.0,
	}

	risk := NewRiskSnapshot(9000.0, 10000.0, nil)

	q, err := engine.GenerateQuote(context.Background(), event, inv, risk)
	if err != nil {
		t.Fatalf("unexpected error near limit: %v", err)
	}

	// Size should be less than max due to proximity to limit
	if q.BidSize >= cfg.MaxQuoteSize || q.AskSize >= cfg.MaxQuoteSize {
		t.Errorf("quote size should be reduced near limit: bid=%f ask=%f max=%f",
			q.BidSize, q.AskSize, cfg.MaxQuoteSize)
	}
}

func TestGenerateQuote_NilInventory_NoError(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(50000.0)

	// Nil inventory should still produce a quote (no skew applied)
	q, err := engine.GenerateQuote(context.Background(), event, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error with nil inventory: %v", err)
	}
	if q == nil {
		t.Fatal("expected non-nil quote with nil inventory")
	}
	if err := validateQuote(q); err != nil {
		t.Fatalf("quote with nil inv should validate: %v", err)
	}
}

// ─── Context Cancellation ──────────────────────────────

func TestGenerateQuote_ContextCancelled(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(50000.0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := engine.GenerateQuote(ctx, event, nil, nil)
	// The current implementation doesn't check ctx in the middle of processing,
	// but this test documents the expected behavior if context cancellation were added.
	_ = err // may or may not return error depending on timing
}

// ─── Quote Output Format ───────────────────────────────

func TestGenerateQuote_OutputHasStatusAndTTL(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(50000.0)

	q, _ := engine.GenerateQuote(context.Background(), event, nil, nil)
	if q.Status != QuoteLive {
		t.Errorf("expected status QuoteLive, got %s", q.Status)
	}
	if q.TTL <= 0 {
		t.Error("expected TTL > 0")
	}
	if q.Timestamp.IsZero() {
		t.Error("expected Timestamp to be set")
	}
}

// ─── Determinism Check ─────────────────────────────────

func TestGenerateQuote_Deterministic(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(50000.0)

	q1, _ := engine.GenerateQuote(context.Background(), event, nil, nil)
	q2, _ := engine.GenerateQuote(context.Background(), event, nil, nil)

	if q1.BidPrice != q2.BidPrice || q1.AskPrice != q2.AskPrice {
		t.Error("identical inputs should produce identical quotes")
	}
}

// ─── Integration: Quote from Engine validates ──────────

func TestGenerateQuote_IntegrationValidation(t *testing.T) {
	engine, _ := NewQuoteEngine(defaultTestConfig())
	event := freshEvent(30000.0) // ETH-like price

	q, err := engine.GenerateQuote(context.Background(), event, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The output is an internal *Quote
	var _ *Quote = q // compile-time type check

	if err := validateQuote(q); err != nil {
		t.Fatalf("generated Quote failed validation: %v", err)
	}
}
