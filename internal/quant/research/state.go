// Package research provides the structured research pipeline for quantitative
// stock analysis, including state management, typed schemas, graph execution,
// and markdown rendering of research outputs.
package research

import (
	"encoding/json"
	"time"
)

// ResearchState is the global state container for a research workflow run.
// It holds all intermediate results produced by analysts, debaters, managers,
// and traders throughout the research lifecycle.
type ResearchState struct {
	Symbol            string
	AnalysisDate      time.Time
	Config            *ResearchConfig
	AnalystReports    map[string]*AnalystReport // analyst name -> report
	DebateState       *InvestDebateState
	RiskDebateState   *RiskDebateState
	ResearchPlan      *ResearchPlan
	TraderProposal    *TraderProposal
	PortfolioDecision *PortfolioDecision
	MarketSnapshot    *VerifiedMarketSnapshot
	CurrentStep       string
	StepsCompleted    []string
	Error             error
}

// InvestDebateState tracks the bull/bear debate state during research.
type InvestDebateState struct {
	Round         int
	MaxRounds     int
	BullArguments []string
	BearArguments []string
	Converged     bool
}

// RiskDebateState tracks the aggressive/conservative/neutral risk debate state.
type RiskDebateState struct {
	Round            int
	MaxRounds        int
	AggressiveView   string
	ConservativeView string
	NeutralView      string
	Converged        bool
}

// ResearchConfig holds configuration for a research run.
type ResearchConfig struct {
	SelectedAnalysts  []string
	MaxDebateRounds   int
	MaxRiskRounds     int
	QuickModel        string
	DeepModel         string
	OutputLanguage    string
	DataVendors       []string
	CheckpointEnabled bool
	MemoryEnabled     bool
}

// NewResearchState creates a new ResearchState with initialized fields.
func NewResearchState(symbol string, date time.Time, cfg *ResearchConfig) *ResearchState {
	return &ResearchState{
		Symbol:         symbol,
		AnalysisDate:   date,
		Config:         cfg,
		AnalystReports: make(map[string]*AnalystReport),
		DebateState: &InvestDebateState{
			MaxRounds: defaultMaxRounds(cfg),
		},
		RiskDebateState: &RiskDebateState{
			MaxRounds: defaultMaxRiskRounds(cfg),
		},
		StepsCompleted: make([]string, 0, 12),
	}
}

// Reset clears all mutable state while preserving symbol, date, and config.
// Useful for re-running analysis on the same symbol.
func (s *ResearchState) Reset() {
	s.AnalystReports = make(map[string]*AnalystReport)
	s.DebateState = &InvestDebateState{
		MaxRounds: defaultMaxRounds(s.Config),
	}
	s.RiskDebateState = &RiskDebateState{
		MaxRounds: defaultMaxRiskRounds(s.Config),
	}
	s.ResearchPlan = nil
	s.TraderProposal = nil
	s.PortfolioDecision = nil
	s.MarketSnapshot = nil
	s.CurrentStep = ""
	s.StepsCompleted = make([]string, 0, 12)
	s.Error = nil
}

// Clone creates a deep copy of the research state for checkpoint purposes.
// The returned state is fully independent; mutations do not affect the original.
func (s *ResearchState) Clone() *ResearchState {
	if s == nil {
		return nil
	}
	cloned := &ResearchState{
		Symbol:            s.Symbol,
		AnalysisDate:      s.AnalysisDate,
		Config:            cloneConfig(s.Config),
		AnalystReports:    cloneAnalystReports(s.AnalystReports),
		DebateState:       cloneInvestDebate(s.DebateState),
		RiskDebateState:   cloneRiskDebate(s.RiskDebateState),
		ResearchPlan:      cloneResearchPlan(s.ResearchPlan),
		TraderProposal:    cloneTraderProposal(s.TraderProposal),
		PortfolioDecision: clonePortfolioDecision(s.PortfolioDecision),
		MarketSnapshot:    cloneMarketSnapshot(s.MarketSnapshot),
		CurrentStep:       s.CurrentStep,
		StepsCompleted:    cloneStrings(s.StepsCompleted),
		Error:             s.Error,
	}
	return cloned
}

// ToJSON serializes the research state to JSON for event store persistence.
func (s *ResearchState) ToJSON() ([]byte, error) {
	if s == nil {
		return json.Marshal(nil)
	}
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// FromJSON deserializes a research state from JSON bytes.
func FromJSON(data []byte) (*ResearchState, error) {
	var state ResearchState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// ─── Internal Helpers ──────────────────────────────────────

func defaultMaxRounds(cfg *ResearchConfig) int {
	if cfg != nil && cfg.MaxDebateRounds > 0 {
		return cfg.MaxDebateRounds
	}
	return 3
}

func defaultMaxRiskRounds(cfg *ResearchConfig) int {
	if cfg != nil && cfg.MaxRiskRounds > 0 {
		return cfg.MaxRiskRounds
	}
	return 2
}

func cloneConfig(cfg *ResearchConfig) *ResearchConfig {
	if cfg == nil {
		return nil
	}
	cp := *cfg
	cp.SelectedAnalysts = cloneStrings(cp.SelectedAnalysts)
	cp.DataVendors = cloneStrings(cp.DataVendors)
	return &cp
}

func cloneAnalystReports(m map[string]*AnalystReport) map[string]*AnalystReport {
	if m == nil {
		return nil
	}
	result := make(map[string]*AnalystReport, len(m))
	for k, v := range m {
		result[k] = cloneAnalystReport(v)
	}
	return result
}

func cloneAnalystReport(r *AnalystReport) *AnalystReport {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Findings = cloneFindings(r.Findings)
	return &cp
}

func cloneFindings(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}

func cloneInvestDebate(d *InvestDebateState) *InvestDebateState {
	if d == nil {
		return nil
	}
	cp := *d
	cp.BullArguments = cloneStrings(cp.BullArguments)
	cp.BearArguments = cloneStrings(cp.BearArguments)
	return &cp
}

func cloneRiskDebate(d *RiskDebateState) *RiskDebateState {
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

func cloneResearchPlan(p *ResearchPlan) *ResearchPlan {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

func cloneTraderProposal(p *TraderProposal) *TraderProposal {
	if p == nil {
		return nil
	}
	cp := *p
	return &cp
}

func clonePortfolioDecision(d *PortfolioDecision) *PortfolioDecision {
	if d == nil {
		return nil
	}
	cp := *d
	return &cp
}

func cloneMarketSnapshot(s *VerifiedMarketSnapshot) *VerifiedMarketSnapshot {
	if s == nil {
		return nil
	}
	cp := *s
	cp.OHLCV = cloneCandle(s.OHLCV)
	if s.Indicators != nil {
		cp.Indicators = make(map[string]float64, len(s.Indicators))
		for k, v := range s.Indicators {
			cp.Indicators[k] = v
		}
	}
	cp.RecentCloses = cloneFloat64Slice(s.RecentCloses)
	return &cp
}

func cloneCandle(c *Candle) *Candle {
	if c == nil {
		return nil
	}
	cp := *c
	return &cp
}

func cloneStrings(s []string) []string {
	if s == nil {
		return nil
	}
	cp := make([]string, len(s))
	copy(cp, s)
	return cp
}

func cloneFloat64Slice(s []float64) []float64 {
	if s == nil {
		return nil
	}
	cp := make([]float64, len(s))
	copy(cp, s)
	return cp
}
