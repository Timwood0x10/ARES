// Package main — CLI glue for the investment simulation backtest.
// Core logic is in internal/quant/portfolio; this file re-exports types
// and wraps functions so existing callers (main.go, tests) continue
// to work without import changes.
package main

import (
	"context"

	"goagentx/internal/quant/portfolio"
	"goagentx/internal/quant/research"
)

// TradeSignal re-exported from portfolio package.
type TradeSignal = portfolio.TradeSignal

// SimulationResult re-exported from portfolio package.
type SimulationResult = portfolio.SimulationResult

// InvestmentSimulator re-exported from portfolio package.
type InvestmentSimulator = portfolio.InvestmentSimulator

// GenerateSignalsFromResearch delegates to portfolio.GenerateSignalsFromResearch.
func GenerateSignalsFromResearch(decision *research.PortfolioDecision) []TradeSignal {
	return portfolio.GenerateSignalsFromResearch(decision)
}

// SaveSimulationResult delegates to portfolio.SaveSimulationResult.
func SaveSimulationResult(result *SimulationResult, outPath string) error {
	return portfolio.SaveSimulationResult((*portfolio.SimulationResult)(result), outPath)
}

// RunSimulation is a convenience wrapper that creates a simulator with
// default parameters and runs a backtest.
func RunSimulation(ctx context.Context, ticker string, dataDir string, signals []TradeSignal) (*SimulationResult, error) {
	sim := &portfolio.InvestmentSimulator{
		InitialCapital: 100_000.0,
		PositionSize:   0.10,
		Commission:     0.001,
	}
	return sim.RunSimulation(ctx, ticker, dataDir, signals)
}
