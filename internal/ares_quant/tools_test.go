package ares_quant

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_quant/market"
)

func TestSentimentLabel(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{1.0, SentimentStronglyBullish},
		{0.7, SentimentStronglyBullish},
		{0.69, SentimentBullish},
		{0.6, SentimentBullish},
		{0.59, SentimentNeutral},
		{0.5, SentimentNeutral},
		{0.4, SentimentNeutral},
		{0.39, SentimentBearish},
		{0.3, SentimentBearish},
		{0.29, SentimentStronglyBearish},
		{0.0, SentimentStronglyBearish},
		{-1.0, SentimentStronglyBearish},
	}
	for _, tc := range tests {
		got := sentimentLabel(tc.score)
		assert.Equal(t, tc.want, got, "sentimentLabel(%f)", tc.score)
	}
}

func TestRSISignal(t *testing.T) {
	tests := []struct {
		rsi  float64
		want string
	}{
		{100, RSIOverbought},
		{70, RSIOverbought},
		{69, SentimentNeutral},
		{50, SentimentNeutral},
		{31, SentimentNeutral},
		{30, RSIOversold},
		{0, RSIOversold},
	}
	for _, tc := range tests {
		got := rsiSignal(tc.rsi)
		assert.Equal(t, tc.want, got, "rsiSignal(%f)", tc.rsi)
	}
}

func TestSMAPosition(t *testing.T) {
	assert.Equal(t, "above", smaPosition(110, 100))
	assert.Equal(t, "below", smaPosition(90, 100))
	assert.Equal(t, "at", smaPosition(100, 100))
}

func TestLastVal(t *testing.T) {
	assert.Equal(t, 0.0, lastVal(nil))
	assert.Equal(t, 0.0, lastVal([]float64{}))
	assert.Equal(t, 5.0, lastVal([]float64{1, 2, 3, 5}))
	assert.Equal(t, 42.0, lastVal([]float64{42}))
}

func TestLastN(t *testing.T) {
	// Slice longer than n: returns last n elements.
	got := lastN([]float64{1, 2, 3, 4, 5}, 3)
	assert.Equal(t, []float64{3, 4, 5}, got)
	// Slice shorter than n: returns full slice.
	got = lastN([]float64{1, 2}, 5)
	assert.Equal(t, []float64{1, 2}, got)
	// Empty slice.
	got = lastN(nil, 3)
	assert.Equal(t, []float64(nil), got)
	got = lastN([]float64{}, 3)
	assert.Equal(t, []float64{}, got)
}

func TestExtractCloses(t *testing.T) {
	bars := []market.Candle{
		{Close: 100},
		{Close: 101},
		{Close: 102},
	}
	got := extractCloses(bars)
	assert.Equal(t, []float64{100, 101, 102}, got)
}

func TestExtractCloses_Empty(t *testing.T) {
	assert.Equal(t, []float64{}, extractCloses(nil))
	assert.Equal(t, []float64{}, extractCloses([]market.Candle{}))
}

func TestIndicatorPeriod_Default(t *testing.T) {
	got := indicatorPeriod(map[string]interface{}{})
	assert.Equal(t, 14, got)
}

func TestIndicatorPeriod_WithPeriod(t *testing.T) {
	got := indicatorPeriod(map[string]interface{}{ParamPeriod: float64(21)})
	assert.Equal(t, 21, got)
}

func TestIndicatorPeriod_ZeroOrNegative(t *testing.T) {
	got := indicatorPeriod(map[string]interface{}{ParamPeriod: float64(0)})
	assert.Equal(t, 14, got)
	got = indicatorPeriod(map[string]interface{}{ParamPeriod: float64(-5)})
	assert.Equal(t, 14, got)
}

func TestComputeMACDResult(t *testing.T) {
	prices := make([]float64, 50)
	for i := range prices {
		prices[i] = 100 + float64(i)
	}
	r := computeMACDResult("TEST", prices)
	assert.True(t, r.Success)
	v, ok := r.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "TEST", v[ParamTicker])
	assert.Equal(t, "MACD", v[ParamIndicator])
}

func TestComputeRSIResult(t *testing.T) {
	prices := make([]float64, 20)
	for i := range prices {
		prices[i] = 100 + float64(i)
	}
	r := computeRSIResult("TEST", prices, 14)
	assert.True(t, r.Success)
	v, ok := r.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "RSI", v[ParamIndicator])
	assert.Equal(t, RSIOverbought, v["signal"])
}

func TestComputeSMAResult(t *testing.T) {
	prices := []float64{10, 20, 30, 40, 50}
	r := computeSMAResult("T", prices, 3)
	assert.True(t, r.Success)
	v, ok := r.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "SMA", v[ParamIndicator])
	assert.Equal(t, 3, v[ParamPeriod])
	assert.Equal(t, "above", v["position"])
}

func TestComputeBollingerResult(t *testing.T) {
	prices := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	r := computeBollingerResult("T", prices, 3)
	assert.True(t, r.Success)
	v, ok := r.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "BOLLINGER", v[ParamIndicator])
	assert.Equal(t, 3, v[ParamPeriod])
	width := v["width"].(float64)
	assert.True(t, width >= 0, "bollinger width should be non-negative")
}

func TestComputeBollingerResult_FlatPrices(t *testing.T) {
	prices := []float64{100, 100, 100, 100, 100}
	r := computeBollingerResult("T", prices, 3)
	assert.True(t, r.Success)
	v, ok := r.Data.(map[string]interface{})
	require.True(t, ok)
	// With flat prices, bands collapse to the same value.
	assert.Equal(t, 100.0, v["middle_band"])
	assert.Equal(t, 100.0, v["upper_band"])
	assert.Equal(t, 100.0, v["lower_band"])
}

func TestComputeAllResult(t *testing.T) {
	prices := make([]float64, 60)
	for i := range prices {
		prices[i] = 100 + float64(i)*0.5
	}
	r := computeAllResult("ALL", prices)
	assert.True(t, r.Success)
	v, ok := r.Data.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "ALL", v["ticker"])
	assert.NotNil(t, v["macd"])
	assert.NotNil(t, v["rsi"])
	assert.NotNil(t, v["sma_20"])
	assert.NotNil(t, v["sma_50"])
	assert.NotNil(t, v["bollinger_upper"])
}

// ─── Economic indicator strings ──────────────────────────────

func TestSentimentLabel_EdgeBounds(t *testing.T) {
	// Exact boundary values.
	assert.Equal(t, SentimentStronglyBullish, sentimentLabel(0.7))
	assert.Equal(t, SentimentBullish, sentimentLabel(0.6))
	assert.Equal(t, SentimentNeutral, sentimentLabel(0.4))
	assert.Equal(t, SentimentBearish, sentimentLabel(0.3))
	assert.Equal(t, SentimentStronglyBearish, sentimentLabel(0.0))
}

// ─── TimeSeries stub for calls that need market data ─────────

func TestFinancialDataTool_Config(t *testing.T) {
	tool := financialDataTool()
	assert.Equal(t, "financial_data", tool.Name())
	assert.NotEmpty(t, tool.Description())

	params := tool.Parameters()
	require.NotNil(t, params)
	_, hasTicker := params.Properties[ParamTicker]
	assert.True(t, hasTicker, "financial_data tool must have 'ticker' parameter")
	assert.Contains(t, params.Required, ParamTicker)
}

func TestPolymarketTool_Config(t *testing.T) {
	tool := polymarketTool()
	assert.Equal(t, "polymarket_sentiment", tool.Name())

	params := tool.Parameters()
	require.NotNil(t, params)
	_, hasQuery := params.Properties[ParamQuery]
	assert.True(t, hasQuery, "polymarket_sentiment tool must have 'query' parameter")
	assert.Contains(t, params.Required, ParamQuery)
}

func TestTechnicalIndicatorsTool_Config(t *testing.T) {
	tool := technicalIndicatorsTool()
	assert.Equal(t, "technical_indicators", tool.Name())

	params := tool.Parameters()
	require.NotNil(t, params)
	assert.Contains(t, params.Required, ParamTicker)
	assert.Contains(t, params.Required, ParamIndicator)
}
