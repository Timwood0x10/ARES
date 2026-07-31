package marketmakingapi

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
)

// DataFeed defines the interface for streaming market data.
type DataFeed interface {
	io.Closer
	// Connect establishes the data feed connection and subscribes to symbols.
	Connect(ctx context.Context, symbols []string) error
}

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
// It coordinates research, quoting, risk management, backtesting, and execution
// through injected interfaces — no internal/quant dependency leaks to callers.
type Client struct {
	config         *MarketMakingConfig
	researchEngine ResearchEngine
	quoteEngine    QuoteEngine
	riskManager    RiskManager
	inventoryMgr   InventoryManager
	backtestRunner BacktestRunner
	paperTrader    PaperTrader
	dataFeed       DataFeed
	mu             sync.RWMutex
	started        bool
	stopped        bool
	quoteCtx       context.Context
	stopQuote      context.CancelFunc
	// stopOnce guarantees cleanup runs exactly once regardless of which
	// entry point (Stop or Close) triggers it. stopErr captures the first
	// cleanup error so both entry points can return it consistently.
	stopOnce sync.Once
	stopErr  error
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

// SetBacktestRunner injects a backtest runner implementation.
//
// Args:
//
//	runner - the backtest runner to use for historical simulations.
func (c *Client) SetBacktestRunner(runner BacktestRunner) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.backtestRunner = runner
}

// SetPaperTrader injects a paper trader implementation.
func (c *Client) SetPaperTrader(trader PaperTrader) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.paperTrader = trader
}

// SetDataFeed injects a market data feed implementation.
func (c *Client) SetDataFeed(feed DataFeed) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dataFeed = feed
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

	// Connect data feed
	if c.dataFeed != nil {
		if err := c.dataFeed.Connect(ctx, c.config.Symbols); err != nil {
			return errors.Wrap(err, "connect data feed")
		}
	}

	// Start quote loop in Paper or Live mode. Derive the quote context from
	// the caller's ctx so cancellation propagates and avoids leaking the
	// goroutine when the caller's context expires.
	if c.config.Mode == ModePaper || c.config.Mode == ModeLive {
		c.quoteCtx, c.stopQuote = context.WithCancel(ctx)
		go c.quoteLoop(c.quoteCtx)
	}

	c.started = true
	return nil
}

// quoteLoop periodically generates quotes for each configured symbol.
func (c *Client) quoteLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.RLock()
			qe := c.quoteEngine
			symbols := c.config.Symbols
			c.mu.RUnlock()

			if qe == nil {
				continue
			}
			for _, sym := range symbols {
				_, err := qe.GenerateQuote(ctx, sym)
				if err != nil {
					// Quote engine error logged by caller; skip to next symbol.
					continue
				}
			}
		}
	}
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
	// ctx is reserved for future graceful-shutdown timeouts; cleanup is
	// currently synchronous.
	_ = ctx

	c.mu.Lock()
	if !c.started || c.stopped {
		c.mu.Unlock()
		return nil
	}
	c.stopped = true
	// Cancel the quote loop context while holding the lock. CancelFunc
	// is fast and idempotent; calling c.stopQuote directly (rather than
	// via a local) lets gosec verify the WithCancel is drained.
	if c.stopQuote != nil {
		c.stopQuote()
	}
	dataFeed := c.dataFeed
	c.mu.Unlock()

	// stopOnce guarantees the data feed close runs exactly once, even
	// if Close and Stop are called concurrently. Only the goroutine
	// that flips c.stopped under the lock reaches here, so stopOnce is
	// defense-in-depth against future refactors that might break the
	// single-writer invariant.
	c.stopOnce.Do(func() {
		if dataFeed != nil {
			if err := dataFeed.Close(); err != nil {
				c.stopErr = errors.Wrap(err, "close data feed")
			}
		}
	})
	return c.stopErr
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
	if req.InitialCapital <= 0 {
		return nil, fmt.Errorf("backtest: InitialCapital must be positive, got %.2f", req.InitialCapital)
	}

	// Build effective symbols without mutating the caller's request.
	// When req.Symbols is empty, fall back to config symbols (copied so
	// downstream runners cannot alias or mutate c.config.Symbols).
	c.mu.RLock()
	runner := c.backtestRunner
	symbols := req.Symbols
	if len(symbols) == 0 {
		cfgSymbols := c.config.Symbols
		symbols = make([]string, len(cfgSymbols))
		copy(symbols, cfgSymbols)
	}
	c.mu.RUnlock()

	if runner == nil {
		return nil, ErrNotInitialized
	}

	// Pass a copy of the request with the effective symbols so the caller's
	// original request is not mutated. Reference-typed fields (Symbols, Signals,
	// AssetTypes) are deep-copied so a runner cannot mutate them in place and
	// leak changes back to the caller. TradeSignal is a value-only struct, so a
	// slice copy fully isolates the elements.
	effectiveReq := *req
	effectiveReq.Symbols = symbols
	if req.Signals != nil {
		effectiveReq.Signals = make([]TradeSignal, len(req.Signals))
		copy(effectiveReq.Signals, req.Signals)
	}
	if req.AssetTypes != nil {
		effectiveReq.AssetTypes = make(map[string]string, len(req.AssetTypes))
		for k, v := range req.AssetTypes {
			effectiveReq.AssetTypes[k] = v
		}
	}
	return runner.Run(ctx, &effectiveReq)
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
	if req.InitialCapital <= 0 {
		return nil, fmt.Errorf("paper trade: InitialCapital must be positive, got %.2f", req.InitialCapital)
	}

	// Build effective symbols without mutating the caller's request.
	// When req.Symbols is empty, fall back to config symbols (copied so
	// downstream traders cannot alias or mutate c.config.Symbols).
	c.mu.RLock()
	trader := c.paperTrader
	symbols := req.Symbols
	if len(symbols) == 0 {
		cfgSymbols := c.config.Symbols
		symbols = make([]string, len(cfgSymbols))
		copy(symbols, cfgSymbols)
	}
	c.mu.RUnlock()

	if trader == nil {
		return nil, ErrNotInitialized
	}

	// Pass a shallow copy of the request with the effective symbols so
	// the caller's original request is not mutated.
	effectiveReq := *req
	effectiveReq.Symbols = symbols
	return trader.Start(ctx, &effectiveReq)
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
	// Delegate to Stop — it is idempotent and lock-protected. This
	// avoids the non-atomic wasStarted check that previously read
	// c.started/c.stopped under the lock but called Stop after releasing
	// it, allowing a race with concurrent Stop callers.
	return c.Stop(context.Background())
}
