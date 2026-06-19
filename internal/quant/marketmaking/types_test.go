package marketmaking

import (
	"math"
	"testing"
	"time"
)

// ─── Order State Transition Tests ──────────────────────

func TestOrderTransition_NormalPath(t *testing.T) {
	o := &Order{ID: "ord-1", State: OrderNew}

	if err := o.Transition(OrderAck); err != nil {
		t.Fatalf("new -> ack: unexpected error: %v", err)
	}
	if o.State != OrderAck {
		t.Fatalf("expected state ack, got %s", o.State)
	}

	if err := o.Transition(OrderLive); err != nil {
		t.Fatalf("ack -> live: unexpected error: %v", err)
	}
	if o.State != OrderLive {
		t.Fatalf("expected state live, got %s", o.State)
	}

	if err := o.Transition(OrderFilled); err != nil {
		t.Fatalf("live -> filled: unexpected error: %v", err)
	}
	if o.State != OrderFilled {
		t.Fatalf("expected state filled, got %s", o.State)
	}
}

func TestOrderTransition_PartialFillPath(t *testing.T) {
	o := &Order{ID: "ord-2", State: OrderNew}

	steps := []struct {
		from OrderState
		to   OrderState
	}{
		{OrderNew, OrderAck},
		{OrderAck, OrderLive},
		{OrderLive, OrderPartialFilled},
		{OrderPartialFilled, OrderPartialFilled}, // multiple partial fills allowed
		{OrderPartialFilled, OrderFilled},
	}

	for i, step := range steps {
		if err := o.Transition(step.to); err != nil {
			t.Fatalf("step %d: %s -> %s: unexpected error: %v", i, step.from, step.to, err)
		}
	}
	if o.State != OrderFilled {
		t.Fatalf("expected final state filled, got %s", o.State)
	}
}

func TestOrderTransition_CancelPaths(t *testing.T) {
	tests := []struct {
		name    string
		initial OrderState
		target  OrderState
		wantErr bool
	}{
		{"new -> rejected", OrderNew, OrderRejected, false},
		{"new -> expired", OrderNew, OrderExpired, false},
		{"ack -> cancelled", OrderAck, OrderCancelled, false},
		{"live -> cancelled", OrderLive, OrderCancelled, false},
		{"partial_filled -> cancelled", OrderPartialFilled, OrderCancelled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Order{ID: "ord-cancel", State: tt.initial}
			err := o.Transition(tt.target)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOrderTransition_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name    string
		initial OrderState
		target  OrderState
	}{
		// Cannot skip states
		{"new -> filled (skip ack)", OrderNew, OrderFilled},
		{"new -> live (skip ack)", OrderNew, OrderLive},
		{"ack -> filled (skip live)", OrderAck, OrderFilled},
		{"new -> cancelled (not allowed from new)", OrderNew, OrderCancelled},

		// Terminal states cannot transition out
		{"filled -> live (terminal)", OrderFilled, OrderLive},
		{"filled -> new (terminal)", OrderFilled, OrderNew},
		{"cancelled -> ack (terminal)", OrderCancelled, OrderAck},
		{"rejected -> new (terminal)", OrderRejected, OrderNew},
		{"expired -> ack (terminal)", OrderExpired, OrderAck},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Order{ID: "ord-invalid", State: tt.initial}
			err := o.Transition(tt.target)
			if err == nil {
				t.Fatalf("expected error for %s -> %s, got nil", tt.initial, tt.target)
			}
		})
	}
}

func TestOrderTransition_NilOrder(t *testing.T) {
	var o *Order
	err := o.Transition(OrderAck)
	if err == nil {
		t.Fatal("expected error for nil order")
	}
}

func TestOrderTransition_SameState(t *testing.T) {
	o := &Order{ID: "ord-same", State: OrderNew}
	err := o.Transition(OrderNew)
	if err == nil {
		t.Fatal("expected error for same-state transition")
	}
}

func TestOrderState_IsTerminal(t *testing.T) {
	terminalStates := []OrderState{OrderFilled, OrderCancelled, OrderRejected, OrderExpired}
	nonTerminalStates := []OrderState{OrderNew, OrderAck, OrderLive, OrderPartialFilled}

	for _, s := range terminalStates {
		if !s.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}
	for _, s := range nonTerminalStates {
		if s.IsTerminal() {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}

func TestOrder_RemainingQty(t *testing.T) {
	o := &Order{Quantity: 100.0, FilledQty: 30.0}
	if got := o.RemainingQty(); got != 70.0 {
		t.Fatalf("expected remaining 70.0, got %f", got)
	}

	var nilOrd *Order
	if got := nilOrd.RemainingQty(); got != 0.0 {
		t.Fatalf("expected 0 for nil order, got %f", got)
	}
}

// ─── Inventory Tests ────────────────────────────────────

func TestNewInventory(t *testing.T) {
	inv := NewInventory(10000.0)
	if inv.Cash != 10000.0 {
		t.Fatalf("expected cash 10000.0, got %f", inv.Cash)
	}
	if inv.Positions == nil || len(inv.Positions) != 0 {
		t.Fatal("expected empty positions map")
	}
	if inv.initialCash != 10000.0 {
		t.Fatalf("expected initialCash 10000.0, got %f", inv.initialCash)
	}
}

func TestInventory_TotalEquity_Empty(t *testing.T) {
	inv := NewInventory(10000.0)
	if eq := inv.TotalEquity(); eq != 10000.0 {
		t.Fatalf("expected equity 10000.0 with no positions, got %f", eq)
	}
}

func TestInventory_TotalEquity_WithPositions(t *testing.T) {
	inv := NewInventory(10000.0)
	inv.Positions["AAPL"] = &Position{
		Symbol:        "AAPL",
		Quantity:      10.0,
		AvgEntryPrice: 150.0,
		CurrentPrice:  155.0,
	}
	inv.Positions["TSLA"] = &Position{
		Symbol:        "TSLA",
		Quantity:      -5.0,
		AvgEntryPrice: 800.0,
		CurrentPrice:  780.0,
	}

	expected := 10000.0 + (10.0 * 155.0) + (-5.0 * 780.0) // 10000 + 1550 - 3900 = 7650
	if eq := inv.TotalEquity(); math.Abs(eq-expected) > 1e-9 {
		t.Fatalf("expected equity %f, got %f", expected, eq)
	}
}

func TestInventory_UpdateMarkToMarket(t *testing.T) {
	inv := NewInventory(10000.0)
	inv.Positions["AAPL"] = &Position{
		Symbol:        "AAPL",
		Quantity:      10.0,
		AvgEntryPrice: 150.0,
		CurrentPrice:  150.0,
	}

	prices := map[string]float64{"AAPL": 160.0}
	inv.UpdateMarkToMarket(prices)

	pos := inv.Positions["AAPL"]
	if pos.CurrentPrice != 160.0 {
		t.Fatalf("expected current price 160.0, got %f", pos.CurrentPrice)
	}
	// Unrealized PnL = (160 - 150) * 10 = 100
	if math.Abs(pos.UnrealizedPnL-100.0) > 1e-9 {
		t.Fatalf("expected unrealized PnL 100.0, got %f", pos.UnrealizedPnL)
	}
	if math.Abs(inv.UnrealizedPnL-100.0) > 1e-9 {
		t.Fatalf("expected total unrealized PnL 100.0, got %f", inv.UnrealizedPnL)
	}
}

func TestInventory_UpdateMarkToMarket_NilOrEmpty(t *testing.T) {
	inv := NewInventory(10000.0)
	// Should not panic on nil inventory or empty prices
	inv.UpdateMarkToMarket(nil)
	inv.UpdateMarkToMarket(map[string]float64{})
}

func TestInventory_NetInventoryValue(t *testing.T) {
	inv := NewInventory(10000.0)
	inv.Positions["AAPL"] = &Position{
		Symbol:       "AAPL",
		Quantity:     10.0,
		CurrentPrice: 155.0,
	}
	inv.Positions["TSLA"] = &Position{
		Symbol:       "TSLA",
		Quantity:     -5.0,
		CurrentPrice: 780.0,
	}

	expected := 10.0*155.0 + (-5.0)*780.0 // 1550 - 3900 = -2350
	if val := inv.NetInventoryValue(); math.Abs(val-expected) > 1e-9 {
		t.Fatalf("expected net inventory value %f, got %f", expected, val)
	}
}

func TestInventory_GetSetPosition(t *testing.T) {
	inv := NewInventory(10000.0)

	if pos := inv.GetPosition("AAPL"); pos != nil {
		t.Fatal("expected nil for non-existent position")
	}

	pos := &Position{Symbol: "AAPL", Quantity: 10.0}
	inv.SetPosition(pos)

	if got := inv.GetPosition("AAPL"); got == nil || got.Symbol != "AAPL" {
		t.Fatalf("expected AAPL position, got %+v", got)
	}
}

func TestInventory_NilSafety(t *testing.T) {
	var inv *Inventory
	if inv.TotalEquity() != 0 {
		t.Fatal("expected 0 for nil inventory TotalEquity")
	}
	if inv.NetInventoryValue() != 0 {
		t.Fatal("expected 0 for nil inventory NetInventoryValue")
	}
	if inv.GetPosition("X") != nil {
		t.Fatal("expected nil for nil inventory GetPosition")
	}
	// SetPosition on nil should not panic
	inv.SetPosition(&Position{Symbol: "X"})
}

// ─── RiskSnapshot Tests ─────────────────────────────────

func TestRiskSnapshot_Utilization(t *testing.T) {
	rs := NewRiskSnapshot(500.0, 1000.0, nil)

	if rs.Utilization != 0.5 {
		t.Fatalf("expected utilization 0.5, got %f", rs.Utilization)
	}
	if rs.IsCritical {
		t.Fatal("expected non-critical at 50% utilization")
	}
}

func TestRiskSnapshot_CriticalAtLimit(t *testing.T) {
	rs := NewRiskSnapshot(1000.0, 1000.0, nil)

	if rs.Utilization != 1.0 {
		t.Fatalf("expected utilization 1.0, got %f", rs.Utilization)
	}
	if !rs.IsCritical {
		t.Fatal("expected critical at 100% utilization")
	}
}

func TestRiskSnapshot_CriticalWithBreaches(t *testing.T) {
	rs := NewRiskSnapshot(100.0, 1000.0, []string{"max_position"})

	if !rs.IsCritical {
		t.Fatal("expected critical when breaches exist")
	}
	if len(rs.Breaches) != 1 || rs.Breaches[0] != "max_position" {
		t.Fatalf("unexpected breaches: %v", rs.Breaches)
	}
}

func TestRiskSnapshot_ZeroLimit(t *testing.T) {
	rs := NewRiskSnapshot(500.0, 0, nil)

	if rs.Utilization != 0 {
		t.Fatalf("expected utilization 0 when limit is 0, got %f", rs.Utilization)
	}
}

// ─── Fill Tests ─────────────────────────────────────────

func TestFill_Notional(t *testing.T) {
	f := &Fill{Price: 100.0, Quantity: 5.0}
	if n := f.Notional(); n != 500.0 {
		t.Fatalf("expected notional 500.0, got %f", n)
	}

	var nilFill *Fill
	if n := nilFill.Notional(); n != 0 {
		t.Fatalf("expected 0 for nil fill, got %f", n)
	}
}

// ─── Quote / QuoteStatus Tests ──────────────────────────

func TestQuoteStatus_String(t *testing.T) {
	tests := []struct {
		status QuoteStatus
		want   string
	}{
		{QuoteLive, "live"},
		{QuoteExpired, "expired"},
		{QuoteCancelled, "cancelled"},
		{QuoteRejected, "rejected"},
		{QuoteStatus(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Errorf("QuoteStatus(%d).String() = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestQuote_Fields(t *testing.T) {
	q := &Quote{
		Symbol:   "BTCUSD",
		BidPrice: 50000.0,
		AskPrice: 50100.0,
		BidSize:  1.0,
		AskSize:  1.0,
		TTL:      5 * time.Second,
		Status:   QuoteLive,
	}

	if q.Symbol != "BTCUSD" {
		t.Fatalf("expected symbol BTCUSD, got %s", q.Symbol)
	}
	if q.BidPrice != 50000.0 {
		t.Fatalf("expected bid price 50000.0, got %f", q.BidPrice)
	}
	if q.AskPrice != 50100.0 {
		t.Fatalf("expected ask price 50100.0, got %f", q.AskPrice)
	}
	if q.TTL != 5*time.Second {
		t.Fatalf("expected TTL 5s, got %v", q.TTL)
	}
}

func TestQuote_Validate(t *testing.T) {
	validQ := &Quote{
		Symbol:   "ETHUSD",
		BidPrice: 3000.0,
		AskPrice: 3010.0,
		BidSize:  0.5,
		AskSize:  0.5,
		TTL:      10 * time.Second,
	}

	if err := validateQuote(validQ); err != nil {
		t.Fatalf("valid quote should pass validation: %v", err)
	}
}

func TestQuote_ValidateInvalidBidGtAsk(t *testing.T) {
	invalidQ := &Quote{
		Symbol:   "BAD",
		BidPrice: 200.0,
		AskPrice: 100.0, // bid > ask is invalid
		BidSize:  1.0,
		AskSize:  1.0,
		TTL:      5 * time.Second,
	}

	if err := validateQuote(invalidQ); err == nil {
		t.Fatal("expected validation error for bid >= ask")
	}
}

// ─── MarketDataEvent Tests ──────────────────────────────

func TestMarketDataEvent_IsStaleData(t *testing.T) {
	event := &MarketDataEvent{
		Timestamp: time.Now().Add(-2 * time.Second),
		IsStale:   false,
	}

	// Within threshold — not stale
	if event.IsStaleData(5 * time.Second) {
		t.Fatal("expected not stale within threshold")
	}

	// Beyond threshold — stale
	if !event.IsStaleData(1 * time.Second) {
		t.Fatal("expected stale beyond threshold")
	}
}

func TestMarketDataEvent_IsStaleData_Flag(t *testing.T) {
	event := &MarketDataEvent{
		Timestamp: time.Now(),
		IsStale:   true,
	}

	if !event.IsStaleData(time.Hour) {
		t.Fatal("expected stale when IsStale flag is true")
	}
}

func TestMarketDataEvent_IsStaleData_Nil(t *testing.T) {
	var event *MarketDataEvent
	if !event.IsStaleData(time.Hour) {
		t.Fatal("expected stale for nil event")
	}
}

// ─── Internal Quote Validation Tests ─────────────────────

func TestValidateQuote_Nil(t *testing.T) {
	if err := validateQuote(nil); err == nil {
		t.Fatal("expected error for nil quote")
	}
}

func TestValidateQuote_EmptySymbol(t *testing.T) {
	q := &Quote{Symbol: "", BidPrice: 100, AskPrice: 101, BidSize: 1, AskSize: 1, TTL: time.Second}
	if err := validateQuote(q); err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestValidateQuote_ZeroPrices(t *testing.T) {
	q := &Quote{Symbol: "X", BidPrice: 0, AskPrice: 101, BidSize: 1, AskSize: 1, TTL: time.Second}
	if err := validateQuote(q); err == nil {
		t.Fatal("expected error for zero bid price")
	}
}

func TestValidateQuery_ZeroTTL(t *testing.T) {
	q := &Quote{Symbol: "X", BidPrice: 100, AskPrice: 101, BidSize: 1, AskSize: 1, TTL: 0}
	if err := validateQuote(q); err == nil {
		t.Fatal("expected error for zero TTL")
	}
}
