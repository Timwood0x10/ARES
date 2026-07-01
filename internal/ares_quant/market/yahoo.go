package market

import (
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	qerrors "github.com/Timwood0x10/ares/internal/ares_quant/errors"
)

// yahooDownloadURL is the Yahoo Finance CSV download endpoint.
// More reliable than the v8 chart JSON API.
const yahooDownloadURL = "https://query1.finance.yahoo.com/v7/finance/download/%s?period1=%d&period2=%d&interval=1d&events=history"

// YahooFeed implements Feed using Yahoo Finance's CSV download API.
type YahooFeed struct {
	client    *http.Client
	AllowMock bool // FIX: controls whether mock data fallback is allowed (default: false)
}

// NewYahooFeed creates a new Yahoo Finance data feed.
func NewYahooFeed() *YahooFeed {
	return &YahooFeed{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (f *YahooFeed) Name() string { return "yahoo" }

// Candles fetches historical OHLCV data.
// When AllowMock is false (default) and the fetch fails, it returns qerrors.ErrNoMarketData
// instead of silently falling back to generated data, preventing LLM from receiving fake data.
func (f *YahooFeed) Candles(ticker string, start, end time.Time, _ Resolution) (TimeSeries, error) {
	bars, err := f.fetchCSV(ticker, start, end)
	if err == nil && len(bars) > 0 {
		return TimeSeries{Ticker: ticker, Bars: bars}, nil
	}

	// FIX: only fall back to mock data when explicitly allowed; otherwise return sentinel error.
	if !f.AllowMock {
		return TimeSeries{}, fmt.Errorf("%w: yahoo fetch failed for ticker %q", qerrors.ErrNoMarketData, ticker)
	}

	bars = generateMockData(ticker, start, end)
	ts := TimeSeries{Ticker: ticker, Bars: bars}
	// Mark as simulated so callers can detect mock data.
	// Reserved for future mock-data marking (e.g., IsSimulated field on TimeSeries).
	// Currently a no-op; callers should check AllowMock config for simulation detection.
	_ = len(ts.Bars) // non-empty check preserved for future mock-data marking (e.g., IsSimulated field on TimeSeries)
	return ts, nil
}

func (f *YahooFeed) fetchCSV(ticker string, start, end time.Time) ([]Candle, error) {
	url := fmt.Sprintf(yahooDownloadURL, ticker, start.Unix(), end.Unix())
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("http: close response body failed", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	return parseYahooCSV(ticker, resp.Body)
}

func parseYahooCSV(ticker string, r io.Reader) ([]Candle, error) {
	reader := csv.NewReader(r)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("csv: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("empty csv")
	}

	var bars []Candle
	for _, row := range records[1:] { // Skip header.
		if len(row) < 6 {
			continue
		}
		// CSV columns: Date,Open,High,Low,Close,Adj Close,Volume
		date, err := time.Parse("2006-01-02", strings.TrimSpace(row[0]))
		if err != nil {
			continue
		}
		open, _ := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		high, _ := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		low, _ := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		close_, _ := strconv.ParseFloat(strings.TrimSpace(row[4]), 64)
		volume, _ := strconv.ParseInt(strings.TrimSpace(row[6]), 10, 64)

		if open == 0 || close_ == 0 {
			continue
		}
		bars = append(bars, Candle{
			Ticker: ticker, Date: date,
			Open: open, High: high, Low: low, Close: close_,
			Volume: volume,
		})
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("no valid bars")
	}
	return bars, nil
}

func (f *YahooFeed) Quote(ticker string) (Quote, error) {
	end := time.Now()
	start := end.AddDate(0, 0, -5)
	ts, err := f.Candles(ticker, start, end, Res1d)
	if err != nil {
		return Quote{}, err
	}
	if len(ts.Bars) == 0 {
		return Quote{}, fmt.Errorf("no data")
	}
	last := ts.Bars[len(ts.Bars)-1]
	return Quote{
		Ticker: ticker, Price: last.Close,
		Volume: last.Volume, Time: last.Date,
	}, nil
}

func (f *YahooFeed) Markets(_ string) ([]Market, error) {
	return nil, fmt.Errorf("not supported")
}

// ─── Mock Data Fallback ────────────────────────────────

// generateMockData creates realistic OHLCV data when Yahoo is unreachable.
// Uses a random walk around a base price, ensuring the demo always works.
func generateMockData(ticker string, start, end time.Time) []Candle {
	// Base prices for common tickers (as of mid-2026).
	basePrices := map[string]float64{
		"AAPL": 245, "MSFT": 415, "GOOG": 168, "GOOGL": 168,
		"AMZN": 228, "TSLA": 355, "META": 520, "NVDA": 880,
		"JPM": 240, "V": 310, "JNJ": 158, "WMT": 72,
		"PG": 172, "XOM": 128, "UNH": 570, "HD": 360,
		"BAC": 40, "DIS": 112, "ADBE": 470, "CRM": 280,
	}

	basePrice := 100.0
	if p, ok := basePrices[ticker]; ok {
		basePrice = p
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano())) // #nosec G404
	var bars []Candle
	current := basePrice

	for d := start; d.Before(end) || d.Equal(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
			continue
		}
		change := (rng.Float64() - 0.48) * current * 0.03
		current += change
		if current < basePrice*0.7 {
			current = basePrice * 0.7
		}
		if current > basePrice*1.3 {
			current = basePrice * 1.3
		}
		spread := current * 0.02 * rng.Float64()
		volume := int64(rng.Float64() * 50_000_000)
		bars = append(bars, Candle{
			Ticker: ticker, Date: d,
			Open:   current - spread/2,
			High:   current + spread,
			Low:    current - spread,
			Close:  current,
			Volume: volume,
		})
	}
	return bars
}
