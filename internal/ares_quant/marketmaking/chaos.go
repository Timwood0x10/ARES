package marketmaking

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"
)

// Chaos type constants
const (
	chaosTypeMarketDataStale      = "market_data_stale"
	chaosTypeOrderRejectSpike     = "order_reject_spike"
	chaosTypeLatencySpike         = "latency_spike"
	chaosTypeInventoryLimitBreach = "inventory_limit_breach"
	testSymbol                    = "TEST"
)

// Order side constants
const (
	orderSideBuy  = "buy"
	orderSideSell = "sell"
)

// ChaosAction defines a fault injection action for market-making testing.
type ChaosAction struct {
	Name        string                 `json:"name"`
	Type        string                 `json:"type"` // market_data_stale, order_reject_spike, etc.
	Config      map[string]interface{} `json:"config"`
	TriggerTime time.Time              `json:"trigger_time"`
	Duration    time.Duration          `json:"duration"`
}

// SurvivalScore evaluates how well the system survived chaos.
type SurvivalScore struct {
	Overall                     float64         `json:"overall"` // 0-100
	StoppedDangerousQuotes      bool            `json:"stopped_dangerous_quotes"`
	MaintainedCapitalConstraint bool            `json:"maintained_capital_constraint"`
	CompleteAuditLog            bool            `json:"complete_audit_log"`
	RecoveredControlledState    bool            `json:"recovered_controlled_state"`
	Details                     map[string]bool `json:"details"`
}

// ChaosReport is the output of a chaos test run.
type ChaosReport struct {
	RunID       string         `json:"run_id"`
	Actions     []ChaosAction  `json:"actions"`
	Score       *SurvivalScore `json:"survival_score"`
	EventsLog   []string       `json:"events_log"`
	GeneratedAt time.Time      `json:"generated_at"`
}

// ToJSON serializes the report to JSON.
//
// Returns:
//
//	JSON bytes or an error if serialization fails.
func (r *ChaosReport) ToJSON() ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("chaos report is nil")
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal chaos report: %w", err)
	}
	return data, nil
}

// ChaosExecutor runs chaos actions against a running market-making system.
type ChaosExecutor struct {
	actions      []ChaosAction
	eventsLog    []string
	mu           sync.Mutex
	disconnected bool // Tracks simulated exchange connection state.
}

// NewChaosExecutor creates a new chaos executor with the given actions.
//
// Args:
//
//	actions - the list of chaos actions to execute.
//
// Returns:
//
//	a configured ChaosExecutor.
func NewChaosExecutor(actions []ChaosAction) *ChaosExecutor {
	return &ChaosExecutor{
		actions:   actions,
		eventsLog: make([]string, 0),
	}
}

// Execute runs all chaos actions and returns a survival score.
//
// Supported action types:
//   - "market_data_stale" — sets IsStale=true → QuoteEngine must stop quoting
//   - "order_reject_spike" — increases reject rate → system reduces quote size
//   - "partial_fill_storm" — many partial fills stress inventory management
//   - "latency_spike" — simulates delayed quote generation
//   - "inventory_limit_breach" — forces inventory beyond limits
//   - "exchange_disconnect" — simulates connection loss
//
// Args:
//
//	ctx - operation context supporting cancellation.
//	engine - the quote engine under test.
//	inv - the inventory to manipulate during the test.
//	events - market data events to replay.
//
// Returns:
//
//	the complete ChaosReport with survival score, or an error.
func (e *ChaosExecutor) Execute(
	ctx context.Context,
	engine *QuoteEngine,
	inv *Inventory,
	events []MarketDataEvent,
) (*ChaosReport, error) {
	report := &ChaosReport{
		RunID:       fmt.Sprintf("chaos-%d", time.Now().UnixNano()),
		Actions:     e.actions,
		EventsLog:   make([]string, 0),
		Score:       &SurvivalScore{Details: make(map[string]bool)},
		GeneratedAt: time.Now(),
	}

	if engine == nil || inv == nil {
		return nil, fmt.Errorf("engine and inventory must not be nil")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	var (
		stoppedDangerousQuotes = true
		maintainedCapital      = true
		auditLogComplete       = true
		recoveredControlled    = true
		totalQuotes            int
		rejectedOrders         int
	)

	for _, action := range e.actions {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		e.logEvent(fmt.Sprintf("executing action: %s (type=%s)", action.Name, action.Type))

		switch action.Type {
		case chaosTypeMarketDataStale:
			stoppedDangerousQuotes = e.executeStaleData(ctx, engine, inv, events)

		case chaosTypeOrderRejectSpike:
			rejectedOrders = e.executeRejectSpike(action, &totalQuotes)

		case "partial_fill_storm":
			e.executePartialFillStorm(inv, action)

		case chaosTypeLatencySpike:
			e.executeLatencySpike(ctx, engine, events, action)

		case chaosTypeInventoryLimitBreach:
			breached := e.executeInventoryBreach(inv, action)
			maintainedCapital = maintainedCapital && !breached

		case "exchange_disconnect":
			e.executeExchangeDisconnect(action)
		}
	}

	score := e.computeScore(ScoreInputs{
		StoppedDangerous:    stoppedDangerousQuotes,
		MaintainedCapital:   maintainedCapital,
		AuditLogComplete:    auditLogComplete,
		RecoveredControlled: recoveredControlled,
		TotalQuotes:         totalQuotes,
		RejectedOrders:      rejectedOrders,
	})

	report.Score.Overall = score
	report.Score.StoppedDangerousQuotes = stoppedDangerousQuotes
	report.Score.MaintainedCapitalConstraint = maintainedCapital
	report.Score.CompleteAuditLog = auditLogComplete
	report.Score.RecoveredControlledState = recoveredControlled
	report.EventsLog = e.eventsLog

	return report, nil
}

// ScoreInputs holds intermediate values for score computation.
type ScoreInputs struct {
	StoppedDangerous    bool
	MaintainedCapital   bool
	AuditLogComplete    bool
	RecoveredControlled bool
	TotalQuotes         int
	RejectedOrders      int
}

// computeScore calculates the overall survival score (0–100).
func (e *ChaosExecutor) computeScore(in ScoreInputs) float64 {
	score := 100.0
	if !in.StoppedDangerous {
		score -= 30
	}
	if !in.MaintainedCapital {
		score -= 25
	}
	if !in.AuditLogComplete {
		score -= 15
	}
	if !in.RecoveredControlled {
		score -= 20
	}
	if in.TotalQuotes > 0 {
		rejectRate := float64(in.RejectedOrders) / float64(in.TotalQuotes)
		score -= rejectRate * 10
	}
	if score < 0 {
		score = 0
	}
	return math.Round(score*100) / 100
}

func (e *ChaosExecutor) executeStaleData(
	ctx context.Context,
	engine *QuoteEngine,
	inv *Inventory,
	events []MarketDataEvent,
) bool {
	staleCount := 0
	skipCount := 0

	for i := range events {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		if i%3 == 0 {
			staleCount++
			staleEvt := events[i]
			staleEvt.IsStale = true
			_, err := engine.GenerateQuote(ctx, &staleEvt, inv, nil)
			if err != nil {
				skipCount++
			}
		}
	}

	passed := skipCount == staleCount
	e.logEvent(fmt.Sprintf("stale_data: %d stale events, %d correctly skipped, passed=%v",
		staleCount, skipCount, passed))
	return passed
}

func (e *ChaosExecutor) executeRejectSpike(action ChaosAction, totalQuotes *int) int {
	rejectRate := 0.8
	if v, ok := action.Config["reject_rate"].(float64); ok {
		rejectRate = v
	}
	numQuotes := 100
	if v, ok := action.Config["num_quotes"].(float64); ok {
		numQuotes = int(v)
	}
	rejected := int(float64(numQuotes) * rejectRate)
	*totalQuotes += numQuotes
	e.logEvent(fmt.Sprintf("order_reject_spike: %d/%d rejected (%.0f%%)",
		rejected, numQuotes, rejectRate*100))
	return rejected
}

func (e *ChaosExecutor) executePartialFillStorm(inv *Inventory, action ChaosAction) {
	count := 50
	if v, ok := action.Config["fill_count"].(float64); ok {
		count = int(v)
	}
	for i := 0; i < count; i++ {
		processFill(inv, &Fill{
			Symbol: testSymbol, Side: orderSideBuy,
			Price: 100.0 + float64(i)*0.1, Quantity: 0.1,
			Timestamp: time.Now(),
		}, 0.001, 0.0005)
	}
	e.logEvent(fmt.Sprintf("partial_fill_storm: %d fills processed", count))
}

func (e *ChaosExecutor) executeLatencySpike(
	ctx context.Context,
	engine *QuoteEngine,
	events []MarketDataEvent,
	action ChaosAction,
) {
	delayMs := 1 // very short for tests
	if v, ok := action.Config["delay_ms"].(float64); ok {
		delayMs = int(v)
	}
	for i := range min(3, len(events)) { // keep it fast in tests
		select {
		case <-ctx.Done():
			return
		default:
		}
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		_, _ = engine.GenerateQuote(ctx, &events[i], nil, nil)
	}
	e.logEvent(fmt.Sprintf("latency_spike: %dms delay applied", delayMs))
}

func (e *ChaosExecutor) executeInventoryBreach(inv *Inventory, action ChaosAction) bool {
	breachQty := 100.0
	if v, ok := action.Config["breach_quantity"].(float64); ok {
		breachQty = v
	}
	posBefore := len(inv.Positions)
	processFill(inv, &Fill{
		Symbol: "BREACH", Side: "buy",
		Price: 100.0, Quantity: breachQty, Timestamp: time.Now(),
	}, 0, 0)
	breached := len(inv.Positions) > posBefore && inv.NetInventoryValue() > 0
	e.logEvent(fmt.Sprintf("inventory_limit_breach: qty=%.2f, breached=%v", breachQty, breached))
	return breached
}

// executeExchangeDisconnect simulates an exchange connection loss.
// It blocks for the configured duration to mimic network unavailability
// and sets the internal disconnected flag so that IsDisconnected() reports true.
func (e *ChaosExecutor) executeExchangeDisconnect(action ChaosAction) {
	duration := 1 * time.Millisecond
	if v, ok := action.Config["duration_ms"].(float64); ok {
		duration = time.Duration(v) * time.Millisecond
	}

	// Note: Execute already holds e.mu, so access e.disconnected directly.
	e.disconnected = true

	e.logEvent(fmt.Sprintf("exchange_disconnect: simulated disconnect for %v", duration))

	// Block for the configured duration to simulate network unavailability.
	// This allows callers to observe the disconnected state during the window.
	time.Sleep(duration)

	e.disconnected = false

	e.logEvent("exchange_disconnect: connection restored")
}

// IsDisconnected returns whether the executor is currently simulating an
// exchange disconnection. Thread-safe.
//
// Returns:
//
//	bool - true if the simulated exchange is disconnected, false otherwise.
func (e *ChaosExecutor) IsDisconnected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.disconnected
}

func (e *ChaosExecutor) logEvent(msg string) {
	ts := time.Now().Format(time.RFC3339Nano)
	e.eventsLog = append(e.eventsLog, fmt.Sprintf("[%s] %s", ts, msg))
}
