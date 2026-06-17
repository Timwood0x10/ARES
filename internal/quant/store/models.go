// Package store provides persistent storage for quant trading data.
// The Store interface abstracts across SQLite (dev) and PostgreSQL (prod).
package store

// Decision records a single trading decision produced by the quant pipeline.
type Decision struct {
	ID              string  `json:"id"`
	Ticker          string  `json:"ticker"`
	DecisionDate    string  `json:"decision_date"`
	Signal          string  `json:"signal"`           // "buy" / "sell" / "hold"
	Confidence      float64 `json:"confidence"`       // 0.0 - 1.0
	Quantity        int     `json:"quantity"`
	Price           float64 `json:"price"`
	Reasoning       string  `json:"reasoning"`         // Full agent reasoning
	AnalystReports  string  `json:"analyst_reports"`   // JSON
	DebateRounds    int     `json:"debate_rounds"`
	RealizedReturn  float64 `json:"realized_return"`   // Filled after trade settles
	AlphaVsSPY      float64 `json:"alpha_vs_spy"`
	Reflection      string  `json:"reflection"`        // Post-trade reflection
	CreatedAt       string  `json:"created_at"`
}

// SignalRecord caches a computed technical indicator value.
type SignalRecord struct {
	Ticker    string  `json:"ticker"`
	Date      string  `json:"date"`
	Indicator string  `json:"indicator"` // "MACD", "RSI", "SMA_20", etc.
	Value     float64 `json:"value"`
	Metadata  string  `json:"metadata,omitempty"` // JSON extension
}
