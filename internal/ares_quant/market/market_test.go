package market

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSentimentSignal_Empty(t *testing.T) {
	assert.Equal(t, 0.5, SentimentSignal(nil))
	assert.Equal(t, 0.5, SentimentSignal([]Market{}))
}

func TestSentimentSignal_SingleMarket(t *testing.T) {
	markets := []Market{
		{Question: "Will AAPL hit 250?", YesPrice: 0.75, Volume: 1000},
	}
	assert.Equal(t, 0.75, SentimentSignal(markets))
}

func TestSentimentSignal_HighestVolume(t *testing.T) {
	markets := []Market{
		{Question: "Low vol", YesPrice: 0.1, Volume: 10},
		{Question: "High vol", YesPrice: 0.9, Volume: 10000},
		{Question: "Med vol", YesPrice: 0.5, Volume: 100},
	}
	assert.Equal(t, 0.9, SentimentSignal(markets))
}

func TestSentimentSignal_TieVolume(t *testing.T) {
	markets := []Market{
		{Question: "First", YesPrice: 0.3, Volume: 100},
		{Question: "Second", YesPrice: 0.8, Volume: 100},
	}
	// First market with highest volume wins; on tie, first is kept.
	got := SentimentSignal(markets)
	assert.Equal(t, 0.3, got)
}

func TestParseFloatSafe_Valid(t *testing.T) {
	assert.InDelta(t, 123.456, parseFloatSafe("123.456"), 1e-9)
	assert.InDelta(t, 0.0, parseFloatSafe("0"), 1e-9)
	assert.InDelta(t, -1.5, parseFloatSafe("-1.5"), 1e-9)
}

func TestParseFloatSafe_Invalid(t *testing.T) {
	assert.Equal(t, 0.0, parseFloatSafe(""))
	assert.Equal(t, 0.0, parseFloatSafe("not-a-number"))
	assert.Equal(t, 0.0, parseFloatSafe("abc123"))
}

func TestURLEncode(t *testing.T) {
	assert.Equal(t, "hello", urlEncode("hello"))
	assert.Equal(t, "AAPL", urlEncode("AAPL"))
	assert.Equal(t, "Fed%20rate%20cut", urlEncode("Fed rate cut"))
	assert.Equal(t, "", urlEncode(""))
}

func TestCoinGeckoDays(t *testing.T) {
	assert.Equal(t, 90, coinGeckoDays(Res1d))
	assert.Equal(t, 90, coinGeckoDays(Res1w))
	assert.Equal(t, 7, coinGeckoDays(Res1h))
	assert.Equal(t, 30, coinGeckoDays(Res1m))
	assert.Equal(t, 30, coinGeckoDays(Res5m))
	assert.Equal(t, 30, coinGeckoDays(Res15m))
}

func TestNewCoinGeckoFeed(t *testing.T) {
	f := NewCoinGeckoFeed()
	assert.Equal(t, "coingecko", f.Name())
}

func TestNewYahooFeed(t *testing.T) {
	f := NewYahooFeed()
	assert.Equal(t, "yahoo", f.Name())
	assert.False(t, f.AllowMock)
}

func TestNewPolymarketFeed(t *testing.T) {
	f := NewPolymarketFeed()
	assert.Equal(t, "polymarket", f.Name())
}

func TestPolymarketFeed_CandlesNotSupported(t *testing.T) {
	f := NewPolymarketFeed()
	_, err := f.Candles("BTC", time.Now(), time.Now(), Res1d)
	assert.ErrorContains(t, err, "not supported")
}

func TestPolymarketFeed_QuoteNotSupported(t *testing.T) {
	f := NewPolymarketFeed()
	_, err := f.Quote("AAPL")
	assert.ErrorContains(t, err, "not supported")
}

func TestCoinGeckoFeed_MarketsNotSupported(t *testing.T) {
	f := NewCoinGeckoFeed()
	_, err := f.Markets("test")
	assert.ErrorContains(t, err, "not supported")
}

func TestYahooFeed_MarketsNotSupported(t *testing.T) {
	f := NewYahooFeed()
	_, err := f.Markets("test")
	assert.ErrorContains(t, err, "not supported")
}

func TestYahooFeed_CandlesFailsWithoutMock(t *testing.T) {
	f := NewYahooFeed() // AllowMock is false by default
	_, err := f.Candles("NONEXISTENT", time.Now(), time.Now(), Res1d)
	assert.ErrorContains(t, err, "no market data")
}

func TestYahooFeed_CandlesWithMock(t *testing.T) {
	f := NewYahooFeed()
	f.AllowMock = true
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	ts, err := f.Candles("AAPL", start, end, Res1d)
	assert.NoError(t, err)
	assert.Equal(t, "AAPL", ts.Ticker)
	assert.Greater(t, len(ts.Bars), 0)
}

func TestYahooFeed_QuoteFailsWithoutMock(t *testing.T) {
	f := NewYahooFeed()
	_, err := f.Quote("NONEXISTENT")
	assert.Error(t, err)
}

func TestCandleConstruction(t *testing.T) {
	now := time.Now()
	c := Candle{
		Ticker: "AAPL",
		Date:   now,
		Open:   100, High: 110, Low: 90, Close: 105,
		Volume: 1000000,
	}
	assert.Equal(t, "AAPL", c.Ticker)
	assert.Equal(t, 105.0, c.Close)
}

func TestQuoteConstruction(t *testing.T) {
	q := Quote{
		Ticker: "MSFT", Price: 415.0,
		Change: 2.5, ChangePct: 0.6,
		Volume: 5000000,
	}
	assert.Equal(t, 415.0, q.Price)
	assert.Equal(t, 2.5, q.Change)
}

func TestResolutionConstants(t *testing.T) {
	assert.Equal(t, Resolution("1m"), Res1m)
	assert.Equal(t, Resolution("5m"), Res5m)
	assert.Equal(t, Resolution("15m"), Res15m)
	assert.Equal(t, Resolution("1h"), Res1h)
	assert.Equal(t, Resolution("1d"), Res1d)
	assert.Equal(t, Resolution("1w"), Res1w)
}

func TestMarketConstruction(t *testing.T) {
	m := Market{
		ID: "0x123", Question: "Will X happen?",
		YesPrice: 0.65, NoPrice: 0.35,
		Volume: 50000, EndDate: "2026-12-31",
	}
	assert.Equal(t, "0x123", m.ID)
	assert.InDelta(t, 0.65, m.YesPrice, 1e-9)
}

func TestGenerateMockData_KnownTicker(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)
	bars := generateMockData("AAPL", start, end)
	assert.Greater(t, len(bars), 0)
	for _, b := range bars {
		assert.Equal(t, "AAPL", b.Ticker)
		assert.Greater(t, b.Close, 0.0)
	}
}

func TestGenerateMockData_UnknownTicker(t *testing.T) {
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 6, 3, 0, 0, 0, 0, time.UTC)
	bars := generateMockData("UNKNOWN", start, end)
	assert.Greater(t, len(bars), 0)
	for _, b := range bars {
		assert.Greater(t, b.Close, 0.0)
	}
}

func TestGenerateMockData_SkipsWeekends(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	bars := generateMockData("MSFT", start, end)
	assert.GreaterOrEqual(t, len(bars), 3)
	assert.LessOrEqual(t, len(bars), 6)
}

func TestCoinGeckoFeed_CandlesInvalidTicker(t *testing.T) {
	f := NewCoinGeckoFeed()
	_, err := f.Candles("INVALID-USD", time.Now(), time.Now(), Res1d)
	assert.ErrorContains(t, err, "unsupported crypto ticker")
}

func TestCoinGeckoFeed_QuoteInvalidTicker(t *testing.T) {
	f := NewCoinGeckoFeed()
	_, err := f.Quote("INVALID-USD")
	assert.ErrorContains(t, err, "unsupported crypto ticker")
}

func TestParseYahooCSV(t *testing.T) {
	csv := `Date,Open,High,Low,Close,Adj Close,Volume
2026-01-02,100.5,101.0,99.5,100.8,100.8,1000000
2026-01-03,101.0,102.0,100.5,101.5,101.5,1200000
`
	bars, err := parseYahooCSV("AAPL", strings.NewReader(csv))
	assert.NoError(t, err)
	assert.Len(t, bars, 2)
	assert.Equal(t, 100.8, bars[0].Close)
	assert.Equal(t, "AAPL", bars[0].Ticker)
	assert.Equal(t, int64(1200000), bars[1].Volume)
}

func TestParseYahooCSV_SkipsBadRows(t *testing.T) {
	csv := `Date,Open,High,Low,Close,Adj Close,Volume
2026-01-02,0,0,0,0,0,1000000
2026-01-03,101.0,102.0,100.5,101.5,101.5,1200000
`
	bars, err := parseYahooCSV("AAPL", strings.NewReader(csv))
	assert.NoError(t, err)
	assert.Len(t, bars, 1) // first row has open=0 so it's skipped
}

func TestParseYahooCSV_Empty(t *testing.T) {
	_, err := parseYahooCSV("AAPL", strings.NewReader("Date,Open,High,Low,Close,Adj Close,Volume\n"))
	assert.ErrorContains(t, err, "empty csv")
}

func TestParseYahooCSV_HeaderOnly(t *testing.T) {
	_, err := parseYahooCSV("AAPL", strings.NewReader("Date,Open,High,Low,Close,Adj Close,Volume"))
	assert.ErrorContains(t, err, "empty csv")
}
