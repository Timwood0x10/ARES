package market

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
)

// coinGeckoOHLCURL is the CoinGecko OHLC endpoint returning [timestamp_ms, open, high, low, close].
const coinGeckoOHLCURL = "https://api.coingecko.com/api/v3/coins/%s/ohlc?days=%d&vs_currency=usd"

// coinGeckoSymbolMap maps Yahoo-style crypto tickers to CoinGecko coin IDs.
var coinGeckoSymbolMap = map[string]string{
	"BTC-USD":   "bitcoin",
	"ETH-USD":   "ethereum",
	"ZEC-USD":   "zcash",
	"SOL-USD":   "solana",
	"XRP-USD":   "ripple",
	"ADA-USD":   "cardano",
	"DOGE-USD":  "dogecoin",
	"DOT-USD":   "polkadot",
	"LINK-USD":  "chainlink",
	"AVAX-USD":  "avalanche-2",
	"MATIC-USD": "matic-network",
	"UNI-USD":   "uniswap",
	"ATOM-USD":  "cosmos",
	"LTC-USD":   "litecoin",
	"BCH-USD":   "bitcoin-cash",
	"SPCX-USD":  "spacex-backpack-securities",
}

// coinGeckoDays determines how many days of data to fetch based on resolution.
func coinGeckoDays(res Resolution) int {
	switch res {
	case Res1d, Res1w:
		return 90
	case Res1h:
		return 7
	default:
		return 30
	}
}

// coinGeckoOHLCBar is one row from CoinGecko's OHLC API: [timestamp_ms, open, high, low, close].
type coinGeckoOHLCBar [5]float64

// CoinGeckoFeed implements Feed using the CoinGecko public API.
// Supports crypto symbols: BTC-USD, ETH-USD, ZEC-USD, etc.
type CoinGeckoFeed struct {
	client *http.Client
}

// NewCoinGeckoFeed creates a new CoinGecko data feed.
func NewCoinGeckoFeed() *CoinGeckoFeed {
	return &CoinGeckoFeed{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// Name returns the feed identifier.
func (f *CoinGeckoFeed) Name() string { return "coingecko" }

// Candles fetches historical OHLCV data from CoinGecko.
// ticker must be in Yahoo-style format: BTC-USD, ETH-USD, etc.
func (f *CoinGeckoFeed) Candles(ticker string, start, end time.Time, res Resolution) (TimeSeries, error) {
	coinID, ok := coinGeckoSymbolMap[ticker]
	if !ok {
		return TimeSeries{}, fmt.Errorf("%w: unsupported crypto ticker %q", errors.ErrNoMarketData, ticker)
	}

	days := coinGeckoDays(res)
	url := fmt.Sprintf(coinGeckoOHLCURL, coinID, days)

	req, err := http.NewRequest(http.MethodGet, url, nil) //nolint:noctx
	if err != nil {
		return TimeSeries{}, fmt.Errorf("coingecko request: %w", err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return TimeSeries{}, fmt.Errorf("coingecko fetch: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("http: close response body failed", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return TimeSeries{}, fmt.Errorf("%w: coingecko returned status %d for %s", errors.ErrNoMarketData, resp.StatusCode, ticker)
	}

	var rawBars []coinGeckoOHLCBar
	if err := json.NewDecoder(resp.Body).Decode(&rawBars); err != nil {
		return TimeSeries{}, fmt.Errorf("coingecko decode: %w", err)
	}

	if len(rawBars) == 0 {
		return TimeSeries{}, fmt.Errorf("%w: no data returned from coingecko for %s", errors.ErrNoMarketData, ticker)
	}

	bars := make([]Candle, 0, len(rawBars))
	for _, raw := range rawBars {
		ts := time.UnixMilli(int64(raw[0]))
		if ts.Before(start) || ts.After(end) {
			continue
		}
		bars = append(bars, Candle{
			Ticker: ticker,
			Date:   ts,
			Open:   raw[1],
			High:   raw[2],
			Low:    raw[3],
			Close:  raw[4],
			Volume: 0,
		})
	}

	return TimeSeries{
		Ticker: ticker,
		Start:  start,
		End:    end,
		Bars:   bars,
	}, nil
}

// Quote returns the latest price by fetching recent candles.
func (f *CoinGeckoFeed) Quote(ticker string) (Quote, error) {
	ts, err := f.Candles(ticker, time.Now().AddDate(0, 0, -7), time.Now(), Res1d)
	if err != nil {
		return Quote{}, err
	}
	if len(ts.Bars) == 0 {
		return Quote{}, fmt.Errorf("%w: no quote data for %s", errors.ErrNoMarketData, ticker)
	}

	last := ts.Bars[len(ts.Bars)-1]
	return Quote{
		Ticker: ticker,
		Price:  last.Close,
		Time:   last.Date,
	}, nil
}

// Markets returns nil since CoinGecko does not provide prediction markets.
func (f *CoinGeckoFeed) Markets(_ string) ([]Market, error) {
	return nil, fmt.Errorf("not supported")
}
