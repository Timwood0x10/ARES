package dataflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/quant/market"
)

// ─── mockVendor implements Vendor for testing ───────────────

type mockVendor struct {
	name       string
	available  bool
	candles    []market.Candle
	candlesErr error
	quote      *market.Quote
	quoteErr   error
}

func (m *mockVendor) Name() string { return m.name }

func (m *mockVendor) Candles(_ context.Context, _ string, _ int) ([]market.Candle, error) {
	return m.candles, m.candlesErr
}

func (m *mockVendor) Quote(_ context.Context, _ string) (*market.Quote, error) {
	return m.quote, m.quoteErr
}

func (m *mockVendor) Available() bool { return m.available }

// ─── NoMarketDataError Tests ────────────────────────────────

func TestNoMarketDataError_Error(t *testing.T) {
	err := &NoMarketDataError{
		Symbol:   "AAPL",
		Vendor:   "yahoo",
		DataType: "candles",
		Message:  "connection timeout",
	}
	msg := err.Error()
	assert.Contains(t, msg, "NO_DATA_AVAILABLE")
	assert.Contains(t, msg, "AAPL")
	assert.Contains(t, msg, "yahoo")
	assert.Contains(t, msg, "Do not estimate")
	assert.Contains(t, msg, "connection timeout")
}

func TestNoMarketDataError_Unwrap(t *testing.T) {
	err := &NoMarketDataError{Symbol: "X", Vendor: "Y", DataType: "Z", Message: "m"}
	assert.True(t, errors.Is(err, ErrNoMarketData))
}

func TestNoMarketDataError_Nil(t *testing.T) {
	var e *NoMarketDataError
	assert.Equal(t, "", e.Error())
}

// ─── Sentinel Error Tests ───────────────────────────────────

func TestSentinelErrors_AreDistinct(t *testing.T) {
	sentinels := []error{
		ErrNoMarketData,
		ErrSymbolEmpty,
		ErrStaleData,
		ErrLookaheadData,
		ErrAllVendorsFailed,
		ErrInvalidSymbol,
	}
	for i, s := range sentinels {
		for j, o := range sentinels {
			if i == j {
				continue
			}
			assert.False(t, errors.Is(s, o), "sentinel %d should not match %d", i, j)
		}
	}
}

// ─── Normalizer Tests ───────────────────────────────────────

func TestNormalizer_Crypto(t *testing.T) {
	n := NewNormalizer()
	result, err := n.Normalize("BTCUSDT")
	require.NoError(t, err)
	assert.Equal(t, "BTC-USD", result)
	assert.Equal(t, "crypto", n.DetectAssetClass("BTCUSDT"))
}

func TestNormalizer_Commodity(t *testing.T) {
	n := NewNormalizer()
	result, err := n.Normalize("XAUUSD")
	require.NoError(t, err)
	assert.Equal(t, "GC=F", result)
	assert.Equal(t, "commodity", n.DetectAssetClass("XAUUSD"))
}

func TestNormalizer_Forex(t *testing.T) {
	n := NewNormalizer()
	result, err := n.Normalize("EURUSD")
	require.NoError(t, err)
	assert.Equal(t, "EURUSD=X", result)
	assert.Equal(t, "forex", n.DetectAssetClass("EURUSD"))
}

func TestNormalizer_Index(t *testing.T) {
	n := NewNormalizer()
	result, err := n.Normalize("SPX500")
	require.NoError(t, err)
	assert.Equal(t, "^GSPC", result)
	assert.Equal(t, "index", n.DetectAssetClass("SPX500"))
}

func TestNormalizer_StockPassThrough(t *testing.T) {
	n := NewNormalizer()

	tests := []string{"AAPL", "0700.HK", "600519.SS"}
	for _, sym := range tests {
		t.Run(sym, func(t *testing.T) {
			result, err := n.Normalize(sym)
			require.NoError(t, err)
			assert.Equal(t, sym, result)
		})
	}
}

func TestNormalizer_DetectStockHeuristics(t *testing.T) {
	n := NewNormalizer()
	assert.Equal(t, "stock", n.DetectAssetClass("0700.HK"))
	assert.Equal(t, "stock", n.DetectAssetClass("600519.SS"))
	assert.Equal(t, "stock", n.DetectAssetClass("MSFT"))
}

func TestNormalizer_EmptyInput(t *testing.T) {
	n := NewNormalizer()
	_, err := n.Normalize("")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrSymbolEmpty))

	_, err = n.Normalize("   ")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrSymbolEmpty))
}

// ─── VendorRouter Tests ─────────────────────────────────────

func TestVendorRouter_Route_Success(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{
		CoreStockAPIs: []string{"yahoo"},
	})
	router.Register(&mockVendor{
		name:      "yahoo",
		available: true,
		candles:   []market.Candle{{Ticker: "AAPL", Close: 180.0}},
	})

	ctx := context.Background()
	result, err := router.Route(ctx, "candles", VendorArgs{Symbol: "AAPL", Days: 30, Method: "candles"})
	require.NoError(t, err)
	assert.Equal(t, "yahoo", result.Vendor)
	assert.NoError(t, result.Error)
	assert.NotNil(t, result.Data)
}

func TestVendorRouter_Route_AllVendorsFail(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{
		CoreStockAPIs: []string{"yahoo", "alpha"},
	})
	router.Register(&mockVendor{name: "yahoo", available: true, candlesErr: errors.New("timeout")})
	router.Register(&mockVendor{name: "alpha", available: true, candlesErr: errors.New("forbidden")})

	ctx := context.Background()
	_, err := router.Route(ctx, "candles", VendorArgs{Symbol: "VOID", Days: 30, Method: "candles"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrAllVendorsFailed))

	var noDataErr *NoMarketDataError
	require.True(t, errors.As(err, &noDataErr))
	assert.Contains(t, noDataErr.Error(), "Do not estimate")
	assert.Equal(t, "VOID", noDataErr.Symbol)
	assert.Equal(t, "all", noDataErr.Vendor)
}

func TestVendorRouter_Route_NoVendorsForMethod(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{}) // empty config

	ctx := context.Background()
	_, err := router.Route(ctx, "candles", VendorArgs{Symbol: "AAPL", Method: "candles"})
	assert.Error(t, err)
	assert.True(t, errors.Is(err, ErrAllVendorsFailed))
}

func TestVendorRouter_Route_UnavailableVendorSkipped(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{
		CoreStockAPIs: []string{"down", "up"},
	})
	router.Register(&mockVendor{name: "down", available: false})
	router.Register(&mockVendor{
		name:      "up",
		available: true,
		candles:   []market.Candle{{Ticker: "AAPL", Close: 100}},
	})

	ctx := context.Background()
	result, err := router.Route(ctx, "candles", VendorArgs{Symbol: "AAPL", Days: 10, Method: "candles"})
	require.NoError(t, err)
	assert.Equal(t, "up", result.Vendor)
}

func TestVendorRouter_Route_UnknownMethod(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{CoreStockAPIs: []string{"y"}})

	ctx := context.Background()
	_, err := router.Route(ctx, "magic", VendorArgs{Symbol: "X", Method: "magic"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown route method")
}

func TestVendorRouter_VendorChainByCategory(t *testing.T) {
	cfg := &RouterConfig{
		CoreStockAPIs:       []string{"a"},
		TechnicalIndicators: []string{"b"},
		Fundamentals:        []string{"c"},
		News:                []string{"d"},
		Macro:               []string{"e"},
		PredictionMarkets:   []string{"f"},
	}
	router := NewVendorRouter(cfg)

	tests := []struct {
		method string
		want   []string
	}{
		{"candles", []string{"a"}},
		{"quote", []string{"a"}},
		{"technical_indicators", []string{"b"}},
		{"fundamentals", []string{"c"}},
		{"news", []string{"d"}},
		{"macro", []string{"e"}},
		{"prediction_markets", []string{"f"}},
		{"unknown_category", nil},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			assert.Equal(t, tt.want, router.vendorChain(tt.method))
		})
	}
}

// ─── Validator Tests ───────────────────────────────────────

func TestValidator_EmptyCandles(t *testing.T) {
	v := NewValidator(&ValidationConfig{AnalysisDate: time.Now()})
	err := v.ValidateOHLCV(nil)
	assert.True(t, errors.Is(err, ErrNoMarketData))

	err = v.ValidateOHLCV([]market.Candle{})
	assert.True(t, errors.Is(err, ErrNoMarketData))
}

func TestValidator_StaleData(t *testing.T) {
	analysisDate := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	staleDate := analysisDate.Add(-6 * 24 * time.Hour) // 6 days old

	v := NewValidator(&ValidationConfig{
		MaxStaleDuration: 5 * 24 * time.Hour,
		AnalysisDate:     analysisDate,
	})
	candles := []market.Candle{{Date: staleDate, Close: 100}}

	err := v.ValidateOHLCV(candles)
	assert.True(t, errors.Is(err, ErrStaleData))
}

func TestValidator_FreshData_OK(t *testing.T) {
	analysisDate := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	freshDate := analysisDate.Add(-1 * 24 * time.Hour) // 1 day old

	v := NewValidator(&ValidationConfig{
		MaxStaleDuration: 5 * 24 * time.Hour,
		AnalysisDate:     analysisDate,
	})
	candles := []market.Candle{{Date: freshDate, Close: 150}}

	err := v.ValidateOHLCV(candles)
	assert.NoError(t, err)
}

func TestValidator_LookaheadData_BacktestMode(t *testing.T) {
	analysisDate := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	futureDate := analysisDate.Add(24 * time.Hour)

	v := NewValidator(&ValidationConfig{
		AnalysisDate:   analysisDate,
		IsBacktestMode: true,
	})
	candles := []market.Candle{
		{Date: analysisDate.Add(-24 * time.Hour), Close: 99},
		{Date: futureDate, Close: 101},
	}

	err := v.ValidateOHLCV(candles)
	assert.True(t, errors.Is(err, ErrLookaheadData))
}

func TestValidator_LookbackOnlyInBacktest(t *testing.T) {
	analysisDate := time.Now().UTC()
	futureDate := analysisDate.Add(1 * time.Hour)

	v := NewValidator(&ValidationConfig{
		AnalysisDate:   analysisDate,
		IsBacktestMode: false,
	})
	candles := []market.Candle{{Date: futureDate, Close: 200}}

	// Not in backtest mode — lookahead check is skipped.
	err := v.ValidateOHLCV(candles)
	assert.NoError(t, err)
}

func TestValidator_NewsLookahead(t *testing.T) {
	analysisDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	newsDates := []time.Time{
		analysisDate.Add(-2 * 24 * time.Hour),
		analysisDate.Add(1 * 24 * time.Hour), // future!
	}

	v := NewValidator(&ValidationConfig{
		AnalysisDate:   analysisDate,
		IsBacktestMode: true,
	})
	err := v.ValidateNewsDates(newsDates)
	assert.True(t, errors.Is(err, ErrLookaheadData))
}

func TestValidator_NewsOK(t *testing.T) {
	analysisDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	dates := []time.Time{
		analysisDate.Add(-5 * 24 * time.Hour),
		analysisDate.Add(-1 * 24 * time.Hour),
	}

	v := NewValidator(&ValidationConfig{
		AnalysisDate:   analysisDate,
		IsBacktestMode: true,
	})
	err := v.ValidateNewsDates(dates)
	assert.NoError(t, err)
}

func TestValidator_DefaultConfig(t *testing.T) {
	v := NewValidator(nil)
	assert.NotZero(t, v.maxStaleDuration)
	assert.False(t, v.isBacktestMode)
}

func TestValidator_ContextCancellation(t *testing.T) {
	v := NewValidator(&ValidationConfig{AnalysisDate: time.Now()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := v.ValidateWithContext(ctx, []market.Candle{{Date: time.Now(), Close: 1}})
	assert.True(t, errors.Is(err, context.Canceled))
}

// ─── SnapshotBuilder Tests ─────────────────────────────────

func makeTestCandles(count int, baseTime time.Time) []market.Candle {
	candles := make([]market.Candle, count)
	for i := range candles {
		candles[i] = market.Candle{
			Ticker: "TEST",
			Date:   baseTime.Add(time.Duration(i) * 24 * time.Hour),
			Close:  float64(100 + i),
		}
	}
	return candles
}

func TestSnapshotBuilder_Build_Success(t *testing.T) {
	now := time.Now().UTC()
	candles := makeTestCandles(60, now.Add(-59*24*time.Hour))

	router := NewVendorRouter(&RouterConfig{CoreStockAPIs: []string{"test"}})
	router.Register(&mockVendor{
		name:      "test",
		available: true,
		candles:   candles,
	})
	val := NewValidator(&ValidationConfig{
		MaxStaleDuration: 5 * 24 * time.Hour,
		AnalysisDate:     now,
		IsBacktestMode:   false,
	})
	builder := NewSnapshotBuilder(router, val)

	snapshot, err := builder.Build(context.Background(), "TEST", now)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	assert.Equal(t, now, snapshot.RequestedDate)
	assert.NotNil(t, snapshot.OHLCV)
	assert.Greater(t, len(snapshot.RecentCloses), 0)
	assert.LessOrEqual(t, len(snapshot.RecentCloses), recentCloseCount)
	assert.Contains(t, snapshot.Warning, "conflicting tool output")
	assert.NotEmpty(t, snapshot.Indicators)
}

func TestSnapshotBuilder_Build_EmptySymbol(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{})
	val := NewValidator(nil)
	builder := NewSnapshotBuilder(router, val)

	_, err := builder.Build(context.Background(), "", time.Now())
	assert.True(t, errors.Is(err, ErrSymbolEmpty))
}

func TestSnapshotBuilder_Build_NoDataFromRouter(t *testing.T) {
	router := NewVendorRouter(&RouterConfig{CoreStockAPIs: []string{"bad"}})
	router.Register(&mockVendor{name: "bad", available: true, candlesErr: ErrNoMarketData})
	val := NewValidator(nil)
	builder := NewSnapshotBuilder(router, val)

	_, err := builder.Build(context.Background(), "MISSING", time.Now())
	assert.Error(t, err)
}

func TestSnapshotWarning_Constant(t *testing.T) {
	assert.Contains(t, SnapshotWarning, "flagged")
	assert.Contains(t, SnapshotWarning, "imagination")
}
