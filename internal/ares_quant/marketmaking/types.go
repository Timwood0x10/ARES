// Package marketmaking provides the core domain model for market-making operations.
// It defines internal types for quotes, orders, fills, inventory, risk, and market data events.
package marketmaking

import (
	"fmt"
	"time"
)

// ─── Quote ──────────────────────────────────────────────

// Quote represents a live two-sided quote issued by the market maker.
type Quote struct {
	Symbol    string        `json:"symbol"`
	BidPrice  float64       `json:"bid_price"`
	AskPrice  float64       `json:"ask_price"`
	BidSize   float64       `json:"bid_size"`
	AskSize   float64       `json:"ask_size"`
	Timestamp time.Time     `json:"timestamp"`
	TTL       time.Duration `json:"ttl"`
	Status    QuoteStatus   `json:"status"`
}

// QuoteStatus represents the lifecycle state of a quote.
type QuoteStatus int

const (
	// QuoteLive indicates the quote is active and tradeable.
	QuoteLive QuoteStatus = iota
	// QuoteExpired indicates the quote has passed its TTL.
	QuoteExpired
	// QuoteCancelled indicates the quote was cancelled by the maker.
	QuoteCancelled
	// QuoteRejected indicates the quote was rejected by the exchange or gateway.
	QuoteRejected
)

// String returns a human-readable representation of the quote status.
func (s QuoteStatus) String() string {
	switch s {
	case QuoteLive:
		return "live"
	case QuoteExpired:
		return "expired"
	case QuoteCancelled:
		return "cancelled"
	case QuoteRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// ─── Order ──────────────────────────────────────────────

// OrderState represents the lifecycle state of an order.
type OrderState int

const (
	// OrderNew is the initial state after order creation.
	OrderNew OrderState = iota
	// OrderAck indicates the exchange has acknowledged the order.
	OrderAck
	// OrderLive indicates the order is resting on the book and tradeable.
	OrderLive
	// OrderPartialFilled indicates the order has been partially filled.
	OrderPartialFilled
	// OrderFilled indicates the order has been fully filled.
	OrderFilled
	// OrderCancelled indicates the order was cancelled.
	OrderCancelled
	// OrderRejected indicates the order was rejected by the exchange.
	OrderRejected
	// OrderExpired indicates the order has expired without being filled.
	OrderExpired
)

// String returns a human-readable representation of the order state.
func (s OrderState) String() string {
	switch s {
	case OrderNew:
		return "new"
	case OrderAck:
		return "ack"
	case OrderLive:
		return "live"
	case OrderPartialFilled:
		return "partial_filled"
	case OrderFilled:
		return "filled"
	case OrderCancelled:
		return "cancelled"
	case OrderRejected:
		return "rejected"
	case OrderExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// ValidOrderTransitions defines allowed state transitions for orders.
var ValidOrderTransitions = map[OrderState][]OrderState{
	OrderNew:           {OrderAck, OrderRejected, OrderExpired},
	OrderAck:           {OrderLive, OrderCancelled},
	OrderLive:          {OrderPartialFilled, OrderFilled, OrderCancelled},
	OrderPartialFilled: {OrderPartialFilled, OrderFilled, OrderCancelled},
	// Filled/Cancelled/Rejected/Expired are terminal — no transitions out
}

// IsTerminal returns true if the order state has no valid transitions out.
func (s OrderState) IsTerminal() bool {
	_, ok := ValidOrderTransitions[s]
	return !ok
}

// Order represents a market-making order with full lifecycle tracking.
type Order struct {
	ID           string     `json:"id"`
	Symbol       string     `json:"symbol"`
	Side         string     `json:"side"` // "buy" or "sell"
	Price        float64    `json:"price"`
	Quantity     float64    `json:"quantity"`
	FilledQty    float64    `json:"filled_qty"`
	AvgFillPrice float64    `json:"avg_fill_price"`
	State        OrderState `json:"state"`
	CreateTime   time.Time  `json:"create_time"`
	UpdateTime   time.Time  `json:"update_time"`
}

// Transition moves the order to a new state if the transition is valid.
//
// Returns an error if the transition is not allowed per ValidOrderTransitions.
func (o *Order) Transition(newState OrderState) error {
	if o == nil {
		return fmt.Errorf("cannot transition a nil order")
	}
	// Allow self-transition for partial_filled (multiple partial fills).
	if o.State == newState && o.State != OrderPartialFilled {
		return fmt.Errorf("order %s: already in state %s", o.ID, o.State)
	}
	allowed, ok := ValidOrderTransitions[o.State]
	if !ok {
		return fmt.Errorf("order %s: current state %s is terminal, no transitions allowed", o.ID, o.State)
	}
	for _, s := range allowed {
		if s == newState {
			o.State = newState
			o.UpdateTime = time.Now()
			return nil
		}
	}
	return fmt.Errorf("order %s: invalid transition from %s to %s", o.ID, o.State, newState)
}

// RemainingQty returns the unfilled quantity of the order.
func (o *Order) RemainingQty() float64 {
	if o == nil {
		return 0
	}
	return o.Quantity - o.FilledQty
}

// ─── Fill ───────────────────────────────────────────────

// Fill represents a single fill event for an order.
type Fill struct {
	OrderID   string    `json:"order_id"`
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"` // "buy" or "sell"
	Price     float64   `json:"price"`
	Quantity  float64   `json:"quantity"`
	Timestamp time.Time `json:"timestamp"`
	Fee       float64   `json:"fee"`
}

// Notional returns the total value of this fill (price × quantity).
func (f *Fill) Notional() float64 {
	if f == nil {
		return 0
	}
	return f.Price * f.Quantity
}

// ─── Inventory ──────────────────────────────────────────

// Inventory tracks current holdings and capital for market making.
type Inventory struct {
	Cash          float64              `json:"cash"`
	Positions     map[string]*Position `json:"positions"`
	RealizedPnL   float64              `json:"realized_pnl"`
	UnrealizedPnL float64              `json:"unrealized_pnl"`
	LastUpdated   time.Time            `json:"last_updated"`
	initialCash   float64
}

// Position tracks a single instrument holding.
type Position struct {
	Symbol        string  `json:"symbol"`
	Quantity      float64 `json:"quantity"` // positive=long, negative=short
	AvgEntryPrice float64 `json:"avg_entry_price"`
	CurrentPrice  float64 `json:"current_price"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
}

// NewInventory creates an Inventory with the given initial cash balance.
func NewInventory(cash float64) *Inventory {
	return &Inventory{
		Cash:        cash,
		Positions:   make(map[string]*Position),
		initialCash: cash,
		LastUpdated: time.Now(),
	}
}

// UpdateMarkToMarket updates all position values at the given current prices.
//
// Args:
//
//	prices: map of symbol -> current market price.
func (inv *Inventory) UpdateMarkToMarket(prices map[string]float64) {
	if inv == nil || len(prices) == 0 {
		return
	}
	totalUnrealized := 0.0
	for sym, pos := range inv.Positions {
		if price, ok := prices[sym]; ok {
			pos.CurrentPrice = price
			pos.UnrealizedPnL = (price - pos.AvgEntryPrice) * pos.Quantity
			totalUnrealized += pos.UnrealizedPnL
		}
	}
	inv.UnrealizedPnL = totalUnrealized
	inv.LastUpdated = time.Now()
}

// TotalEquity returns cash + net asset value (sum of position market values).
func (inv *Inventory) TotalEquity() float64 {
	if inv == nil {
		return 0
	}
	nav := 0.0
	for _, pos := range inv.Positions {
		nav += pos.Quantity * pos.CurrentPrice
	}
	return inv.Cash + nav
}

// NetInventoryValue returns the total value of all positions (long positive, short negative).
func (inv *Inventory) NetInventoryValue() float64 {
	if inv == nil {
		return 0
	}
	val := 0.0
	for _, pos := range inv.Positions {
		val += pos.Quantity * pos.CurrentPrice
	}
	return val
}

// GetPosition returns the position for the given symbol, or nil if not found.
func (inv *Inventory) GetPosition(symbol string) *Position {
	if inv == nil {
		return nil
	}
	return inv.Positions[symbol]
}

// SetPosition sets or updates a position in the inventory.
func (inv *Inventory) SetPosition(pos *Position) {
	if inv == nil || pos == nil {
		return
	}
	inv.Positions[pos.Symbol] = pos
	inv.LastUpdated = time.Now()
}

// ─── RiskSnapshot ───────────────────────────────────────

// RiskSnapshot captures the current risk state of the market maker.
type RiskSnapshot struct {
	Timestamp         time.Time `json:"timestamp"`
	InventoryValue    float64   `json:"inventory_value"`
	MaxInventoryLimit float64   `json:"max_inventory_limit"`
	Utilization       float64   `json:"utilization"` // 0-1
	IsCritical        bool      `json:"is_critical"`
	Breaches          []string  `json:"breaches"`
}

// NewRiskSnapshot creates a RiskSnapshot from inventory value and limit.
func NewRiskSnapshot(inventoryValue, maxLimit float64, breaches []string) *RiskSnapshot {
	rs := &RiskSnapshot{
		Timestamp:         time.Now(),
		InventoryValue:    inventoryValue,
		MaxInventoryLimit: maxLimit,
		Breaches:          breaches,
	}
	if maxLimit > 0 {
		rs.Utilization = inventoryValue / maxLimit
	}
	rs.IsCritical = rs.Utilization >= 1.0 || len(breaches) > 0
	return rs
}

// ─── MarketDataEvent ────────────────────────────────────

// MarketDataEvent represents a single tick or bar from the data feed.
type MarketDataEvent struct {
	Symbol    string    `json:"symbol"`
	BidPrice  float64   `json:"bid_price,omitempty"`
	AskPrice  float64   `json:"ask_price,omitempty"`
	MidPrice  float64   `json:"mid_price"`
	Spread    float64   `json:"spread"`
	Volume    int64     `json:"volume"`
	Timestamp time.Time `json:"timestamp"`
	IsStale   bool      `json:"is_stale"`
}

// IsStaleData checks whether the event is older than the given threshold.
func (e *MarketDataEvent) IsStaleData(threshold time.Duration) bool {
	if e == nil || e.IsStale {
		return true
	}
	return time.Since(e.Timestamp) > threshold
}
