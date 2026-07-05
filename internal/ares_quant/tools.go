// Package ares_quant provides MCP-compatible tools for quantitative trading analysis.
// Data sources (Yahoo Finance, Polymarket) and computations (technical indicators)
// are wrapped as MCP Tool instances registered in the global tool Registry.
//
// Usage in an Agent:
//
//	req := dashboard.AgentRequest{
//	    MCPTool: "financial_data",
//	    MCPArgs: map[string]any{"ticker": "AAPL"},
//	}
package ares_quant

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_quant/indicators"
	"github.com/Timwood0x10/ares/internal/ares_quant/market"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// Schema type constants.
const (
	SchemaTypeObject = "object"
	SchemaTypeString = "string"
	SchemaTypeNumber = "number"
)

// Sentiment label constants.
const (
	SentimentStronglyBullish = "strongly_bullish"
	SentimentBullish         = "bullish"
	SentimentNeutral         = "neutral"
	SentimentBearish         = "bearish"
	SentimentStronglyBearish = "strongly_bearish"
)

// Parameter name constants.
const (
	ParamTicker    = "ticker"
	ParamIndicator = "indicator"
	ParamPeriod    = "period"
	ParamQuery     = "query"
)

// RSI signal constants.
const (
	RSIOverbought = "overbought"
	RSIOversold   = "oversold"
)

// RegisterTools registers all ares_quant MCP tools into the given registry.
// Call during application startup before creating any ares_quant agents.
// Returns the first registration error, if any.
func RegisterTools(registry *core.Registry) error {
	tools := []core.Tool{financialDataTool(), polymarketTool(), technicalIndicatorsTool()}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}

// financialDataTool creates the financial_data MCP tool.
func financialDataTool() core.Tool {
	return base.NewToolFunc(
		"financial_data",
		"Fetch stock financial data and recent prices from Yahoo Finance. Returns OHLCV data for the specified ticker and date range.",
		&core.ParameterSchema{
			Type: SchemaTypeObject,
			Properties: map[string]*core.Parameter{
				ParamTicker: {Type: SchemaTypeString, Description: "Stock ticker symbol (e.g. AAPL, MSFT, 0700.HK)"},
				"days":      {Type: SchemaTypeNumber, Description: "Number of historical days to fetch (default 365)"},
			},
			Required: []string{ParamTicker},
		},
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			ticker, ok := params[ParamTicker].(string)
			if !ok || ticker == "" {
				return core.NewErrorResult("financial_data: ticker is required"), nil
			}
			days := 365
			if d, ok := params["days"].(float64); ok && d > 0 {
				days = int(d)
			}
			feed := market.NewYahooFeed()
			end := time.Now()
			start := end.AddDate(0, 0, -days)
			ts, err := feed.Candles(ticker, start, end, market.Res1d)
			if err != nil {
				return core.NewErrorResult(fmt.Sprintf("financial_data: %v", err)), nil
			}
			return core.NewResult(true, map[string]interface{}{
				ParamTicker: ticker,
				"bars":      ts.Bars,
				"bar_count": len(ts.Bars),
				"start":     ts.Start.Format("2006-01-02"),
				"end":       ts.End.Format("2006-01-02"),
			}), nil
		},
	)
}

// polymarketTool creates the polymarket_sentiment MCP tool.
func polymarketTool() core.Tool {
	return base.NewToolFunc(
		"polymarket_sentiment",
		"Fetch prediction market probabilities related to a stock or event. Returns YES/NO prices (0.0-1.0) representing market consensus probability.",
		&core.ParameterSchema{
			Type: SchemaTypeObject,
			Properties: map[string]*core.Parameter{
				ParamQuery: {Type: SchemaTypeString, Description: "Search query (e.g. 'AAPL', 'Fed rate cut', 'inflation')"},
			},
			Required: []string{ParamQuery},
		},
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			query, ok := params[ParamQuery].(string)
			if !ok || query == "" {
				return core.NewErrorResult("polymarket_sentiment: query is required"), nil
			}
			feed := market.NewPolymarketFeed()
			markets, err := feed.Markets(query)
			if err != nil {
				return core.NewErrorResult(fmt.Sprintf("polymarket_sentiment: %v", err)), nil
			}
			signal := market.SentimentSignal(markets)
			return core.NewResult(true, map[string]interface{}{
				ParamQuery:        query,
				"markets":         markets,
				"market_count":    len(markets),
				"sentiment":       signal,
				"sentiment_label": sentimentLabel(signal),
			}), nil
		},
	)
}

func sentimentLabel(v float64) string {
	switch {
	case v >= 0.7:
		return SentimentStronglyBullish
	case v >= 0.6:
		return SentimentBullish
	case v >= 0.4:
		return SentimentNeutral
	case v >= 0.3:
		return SentimentBearish
	default:
		return SentimentStronglyBearish
	}
}

// technicalIndicatorsTool creates the technical_indicators MCP tool.
func technicalIndicatorsTool() core.Tool {
	return base.NewToolFunc(
		"technical_indicators",
		"Compute technical indicators (MACD, RSI, SMA, Bollinger Bands) for a ticker. Requires price data fetched first via financial_data.",
		&core.ParameterSchema{
			Type: SchemaTypeObject,
			Properties: map[string]*core.Parameter{
				ParamTicker:    {Type: SchemaTypeString, Description: "Stock ticker symbol"},
				ParamIndicator: {Type: SchemaTypeString, Description: "Indicator type: 'MACD', 'RSI', 'SMA', 'BOLLINGER', or 'ALL'"},
				ParamPeriod:    {Type: SchemaTypeNumber, Description: "Lookback period (default 14 for RSI, 20 for SMA/Bollinger)"},
			},
			Required: []string{ParamTicker, ParamIndicator},
		},
		func(ctx context.Context, params map[string]interface{}) (core.Result, error) {
			ticker, _ := params[ParamTicker].(string)
			indicatorType, _ := params[ParamIndicator].(string)
			if ticker == "" || indicatorType == "" {
				return core.NewErrorResult("technical_indicators: ticker and indicator are required"), nil
			}
			feed := market.NewYahooFeed()
			end := time.Now()
			start := end.AddDate(0, 0, -180)
			ts, err := feed.Candles(ticker, start, end, market.Res1d)
			if err != nil {
				return core.NewErrorResult(fmt.Sprintf("technical_indicators: fetch data: %v", err)), nil
			}
			prices := extractCloses(ts.Bars)
			period := indicatorPeriod(params)

			switch indicatorType {
			case "MACD":
				return computeMACDResult(ticker, prices), nil
			case "RSI":
				return computeRSIResult(ticker, prices, period), nil
			case "SMA":
				return computeSMAResult(ticker, prices, period), nil
			case "BOLLINGER":
				return computeBollingerResult(ticker, prices, period), nil
			case "ALL":
				return computeAllResult(ticker, prices), nil
			default:
				return core.NewErrorResult(fmt.Sprintf("technical_indicators: unknown indicator '%s'", indicatorType)), nil
			}
		},
	)
}

func extractCloses(bars []market.Candle) []float64 {
	p := make([]float64, len(bars))
	for i, b := range bars {
		p[i] = b.Close
	}
	return p
}

func indicatorPeriod(params map[string]interface{}) int {
	if p, ok := params[ParamPeriod].(float64); ok && p > 0 {
		return int(p)
	}
	return 14
}

func computeMACDResult(ticker string, prices []float64) core.Result {
	macdLine, signal, hist := indicators.MACD(prices, 12, 26, 9)
	return core.NewResult(true, map[string]interface{}{
		"ticker":        ticker,
		"indicator":     "MACD",
		"macd_line":     lastN(macdLine, 5),
		"signal_line":   lastN(signal, 5),
		"histogram":     lastN(hist, 5),
		"latest_macd":   lastVal(macdLine),
		"latest_signal": lastVal(signal),
		"latest_hist":   lastVal(hist),
	})
}

func computeRSIResult(ticker string, prices []float64, period int) core.Result {
	rsi := indicators.RSI(prices, period)
	v := lastVal(rsi)
	return core.NewResult(true, map[string]interface{}{
		"ticker":    ticker,
		"indicator": "RSI",
		"period":    period,
		"latest":    v,
		"signal":    rsiSignal(v),
	})
}

func computeSMAResult(ticker string, prices []float64, period int) core.Result {
	sma := indicators.SMA(prices, period)
	v := lastVal(sma)
	cp := lastVal(prices)
	return core.NewResult(true, map[string]interface{}{
		"ticker":    ticker,
		"indicator": "SMA",
		"period":    period,
		"value":     v,
		"price":     cp,
		"position":  smaPosition(cp, v),
	})
}

func computeBollingerResult(ticker string, prices []float64, period int) core.Result {
	upper, lower, mid := indicators.BollingerBands(prices, period, 2.0)
	u := lastVal(upper)
	l := lastVal(lower)
	m := lastVal(mid)
	cp := lastVal(prices)
	pct := 50.0
	if u-l > 0 {
		pct = (cp - l) / (u - l) * 100
	}
	return core.NewResult(true, map[string]interface{}{
		"ticker":       ticker,
		"indicator":    "BOLLINGER",
		"period":       period,
		"upper_band":   u,
		"middle_band":  m,
		"lower_band":   l,
		"width":        u - l,
		"position_pct": pct,
	})
}

func computeAllResult(ticker string, prices []float64) core.Result {
	macdLine, sigLine, hist := indicators.MACD(prices, 12, 26, 9)
	rsi := indicators.RSI(prices, 14)
	sma20 := indicators.SMA(prices, 20)
	sma50 := indicators.SMA(prices, 50)
	upper, lower, mid := indicators.BollingerBands(prices, 20, 2.0)
	cp := lastVal(prices)
	return core.NewResult(true, map[string]interface{}{
		"ticker":          ticker,
		"price":           cp,
		"macd":            lastVal(macdLine),
		"macd_signal":     lastVal(sigLine),
		"macd_hist":       lastVal(hist),
		"rsi":             lastVal(rsi),
		"rsi_signal":      rsiSignal(lastVal(rsi)),
		"sma_20":          lastVal(sma20),
		"sma_50":          lastVal(sma50),
		"sma_position":    smaPosition(cp, lastVal(sma20)),
		"bollinger_upper": lastVal(upper),
		"bollinger_lower": lastVal(lower),
		"bollinger_mid":   lastVal(mid),
		"bollinger_width": lastVal(upper) - lastVal(lower),
	})
}

func lastVal(f []float64) float64 {
	if len(f) == 0 {
		return 0
	}
	return f[len(f)-1]
}

func lastN(f []float64, n int) []float64 {
	if len(f) <= n {
		return f
	}
	return f[len(f)-n:]
}

func rsiSignal(v float64) string {
	switch {
	case v >= 70:
		return RSIOverbought
	case v <= 30:
		return RSIOversold
	default:
		return SentimentNeutral
	}
}

func smaPosition(price, sma float64) string {
	switch {
	case price > sma:
		return "above"
	case price < sma:
		return "below"
	default:
		return "at"
	}
}
