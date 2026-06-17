package marketmaking

import (
	"context"
	"fmt"
	"sync"

	"goagentx/internal/errors"
)

// ResearchEngine defines the interface for strategy research and signal generation.
// Implementations are injected via the Client constructor.
type ResearchEngine interface {
	// Analyze generates trading signals for the given symbol.
	Analyze(ctx context.Context, symbol string) (Signal, error)
}

// QuoteEngine defines the interface for producing two-sided quotes.
// Implementations are injected via the Client constructor.
type QuoteEngine interface {
	// GenerateQuote produces a two-sided quote for the given symbol.
	GenerateQuote(ctx context.Context, symbol string) (*QuoteDecision, error)
}

// RiskManager defines the interface for risk assessment and position limits.
// Implementations are injected via the Client constructor.
type RiskManager interface {
	// CheckPreTrade evaluates whether a proposed order passes risk limits.
	CheckPreTrade(ctx context.Context, symbol string, side string, qty float64) error
	// GetReport returns a snapshot of current risk exposure.
	GetReport(ctx context.Context) (*RiskReport, error)
}

// InventoryManager defines the interface for tracking positions and cash balance.
type InventoryManager interface {
	// GetPositions returns current inventory state.
	GetPositions(ctx context.Context) (*InventoryReport, error)
}

// Signal represents a trading signal produced by the research engine.
type Signal struct {
	Symbol     string  `json:"symbol"`
	Direction  string  `json:"direction"` // "long", "short", "neutral"
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
}

// Client is the top-level facade for the market-making system.
// It coordinates research, quoting, risk management, and execution through
// injected interfaces — no internal/quant dependency leaks to callers.
type Client struct {
	config         *MarketMakingConfig
	researchEngine ResearchEngine
	quoteEngine    QuoteEngine
	riskManager    RiskManager
	inventoryMgr   InventoryManager
	mu             sync.RWMutex
	started        bool
	stopped        bool
}

// NewClient creates a new market-making Client with the given configuration.
// All engine/manager interfaces are optional; methods that require a missing
// interface will return an appropriate error.
//
// Args:
//
//	cfg - market making configuration, must pass Validate.
//
// Returns:
//
//	client - initialized client instance.
//	err - validation error or nil.
func NewClient(cfg *MarketMakingConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, errors.Wrap(err, "validate config")
	}
	return &Client{
		config: cfg,
	}, nil
}

// SetResearchEngine injects a research engine implementation.
//
// Args:
//
//	engine - the research engine to use for signal generation.
func (c *Client) SetResearchEngine(engine ResearchEngine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.researchEngine = engine
}

// SetQuoteEngine injects a quote engine implementation.
//
// Args:
//
//	engine - the quote engine to use for two-sided quoting.
func (c *Client) SetQuoteEngine(engine QuoteEngine) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.quoteEngine = engine
}

// SetRiskManager injects a risk manager implementation.
//
// Args:
//
//	rm - the risk manager to use for pre-trade checks.
func (c *Client) SetRiskManager(rm RiskManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.riskManager = rm
}

// SetInventoryManager injects an inventory manager implementation.
//
// Args:
//
//	mgr - the inventory manager to use for position tracking.
func (c *Client) SetInventoryManager(mgr InventoryManager) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inventoryMgr = mgr
}

// Start initializes and starts all subsystems: data connections, quote loops,
// and risk monitors according to the configured Mode.
//
// Args:
//
//	ctx - operation context for cancellation.
//
// Returns:
//
//	err - nil on success, or an error if any subsystem fails to start.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return fmt.Errorf("client already started")
	}
	if c.stopped {
		return fmt.Errorf("client has been stopped, cannot restart")
	}

	// TODO: start data feed connection based on c.config.DataSource.
	// Expected behavior: connect to the configured vendor (e.g., Binance),
	// subscribe to market data for all symbols in c.config.Symbols,
	// and begin feeding the quote engine with tick data.

	// TODO: start quote generation loop if mode is Paper or Live.
	// Expected behavior: spawn a goroutine (via errgroup) that periodically
	// calls the quote engine for each symbol, checks risk limits, and
	// submits quotes to the execution gateway.

	c.started = true
	return nil
}

// Stop gracefully shuts down all active subsystems.
// It is safe to call Stop multiple times; subsequent calls are no-ops.
//
// Args:
//
//	ctx - shutdown context with timeout.
//
// Returns:
//
//	err - nil on success, or the first shutdown error encountered.
func (c *Client) Stop(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started || c.stopped {
		return nil
	}
	c.stopped = true

	// TODO: stop quote loop, disconnect data feeds, drain pending orders.
	// Expected behavior: cancel the errgroup context managing quote loops,
	// close WebSocket connections to data vendor, wait for in-flight orders
	// to settle within ctx deadline, then release all resources.

	return nil
}

// Quote produces a two-sided quote for the given symbol using the injected
// quote engine. If no quote engine is set, returns ErrNotInitialized.
//
// Args:
//
//	ctx - operation context.
//	symbol - the instrument to quote.
//
// Returns:
//
//	decision - the quote decision with bid/ask prices and sizes.
//	err - ErrNotInitialized or quote engine error.
func (c *Client) Quote(ctx context.Context, symbol string) (*QuoteDecision, error) {
	c.mu.RLock()
	engine := c.quoteEngine
	c.mu.RUnlock()

	if engine == nil {
		return nil, ErrNotInitialized
	}
	return engine.GenerateQuote(ctx, symbol)
}

// Backtest runs a historical backtest with the given parameters.
// The backtest engine is created internally; results include PnL, Sharpe,
// drawdown, and per-trade logs.
//
// Args:
//
//	ctx - operation context.
//	req - backtest parameters (symbols, time window, initial capital).
//
// Returns:
//
//	response - detailed backtest results.
//	err - validation error or backtest execution error.
func (c *Client) Backtest(ctx context.Context, req *BacktestRequest) (*BacktestResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("backtest request must not be nil")
	}
	if len(req.Symbols) == 0 {
		req.Symbols = c.config.Symbols
	}
	if req.InitialCapital <= 0 {
		return nil, fmt.Errorf("backtest: InitialCapital must be positive, got %.2f", req.InitialCapital)
	}

	// TODO: wire to internal backtest executor via the BacktestRunner interface.
	// Expected behavior: load historical data for req.Symbols from the data source,
	// run the strategy logic over [StartTime, EndTime], simulate fills at mid-price
	// or with configurable slippage, compute performance metrics, return results.
	// FIX: return ErrNotImplemented instead of zero-value + nil so callers can
	// distinguish "success with empty result" from "feature not wired" (code rule 9).

	return nil, ErrNotImplemented
}

// PaperTrade starts or queries a paper trading session.
// In paper mode, trades are simulated against live market data without
// sending real orders.
//
// Args:
//
//	ctx - operation context.
//	req - paper trade parameters (symbols, capital, duration).
//
// Returns:
//
//	response - current session state with PnL and trade log.
//	err - validation or session error.
func (c *Client) PaperTrade(ctx context.Context, req *PaperTradeRequest) (*PaperTradeResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("paper trade request must not be nil")
	}
	if len(req.Symbols) == 0 {
		req.Symbols = c.config.Symbols
	}
	if req.InitialCapital <= 0 {
		return nil, fmt.Errorf("paper trade: InitialCapital must be positive, got %.2f", req.InitialCapital)
	}

	// TODO: wire to internal paper trading engine via the PaperTrader interface.
	// Expected behavior: create a simulated order book, connect to live data feed,
	// execute strategy logic in simulation mode, track virtual PnL and positions,
	// return real-time session status on each call.
	// FIX: return ErrNotImplemented instead of zero-value + nil so callers can
	// distinguish "success with empty result" from "feature not wired" (code rule 9).

	return nil, ErrNotImplemented
}

// GetRisk returns the current risk report from the injected risk manager.
// If no risk manager is set, returns ErrNotInitialized.
//
// Args:
//
//	ctx - operation context.
//
// Returns:
//
//	report - current risk exposure summary.
//	err - ErrNotInitialized or risk manager error.
func (c *Client) GetRisk(ctx context.Context) (*RiskReport, error) {
	c.mu.RLock()
	rm := c.riskManager
	c.mu.RUnlock()

	if rm == nil {
		return nil, ErrNotInitialized
	}
	return rm.GetReport(ctx)
}

// GetInventory returns the current inventory report from the injected
// inventory manager. If none is set, returns ErrNotInitialized.
//
// Args:
//
//	ctx - operation context.
//
// Returns:
//
//	report - current positions and cash balance.
//	err - ErrNotInitialized or inventory manager error.
func (c *Client) GetInventory(ctx context.Context) (*InventoryReport, error) {
	c.mu.RLock()
	mgr := c.inventoryMgr
	c.mu.RUnlock()

	if mgr == nil {
		return nil, ErrNotInitialized
	}
	return mgr.GetPositions(ctx)
}

// Close releases all resources held by the client. It calls Stop internally
// if the client was started but not stopped.
//
// Returns:
//
//	err - the first error encountered during cleanup, or nil.
func (c *Client) Close() error {
	c.mu.Lock()
	wasStarted := c.started && !c.stopped
	c.mu.Unlock()

	if wasStarted {
		if stopErr := c.Stop(context.Background()); stopErr != nil {
			return stopErr
		}
	}
	return nil
}
