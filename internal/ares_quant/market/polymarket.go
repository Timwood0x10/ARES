package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// PolymarketFeed implements Feed using Polymarket's Gamma API.
// Provides prediction market probabilities as sentiment signals.
type PolymarketFeed struct {
	client  *http.Client
	baseURL string
}

// NewPolymarketFeed creates a new Polymarket data feed.
func NewPolymarketFeed() *PolymarketFeed {
	return &PolymarketFeed{
		client:  &http.Client{Timeout: 15 * time.Second},
		baseURL: "https://gamma-api.polymarket.com",
	}
}

// Name returns "polymarket".
func (f *PolymarketFeed) Name() string { return "polymarket" }

// Candles returns an error — Polymarket does not provide OHLCV data.
func (f *PolymarketFeed) Candles(_ string, _, _ time.Time, _ Resolution) (TimeSeries, error) {
	return TimeSeries{}, fmt.Errorf("polymarket: ohlcv not supported")
}

// Quote returns an error — Polymarket does not provide stock quotes.
func (f *PolymarketFeed) Quote(_ string) (Quote, error) {
	return Quote{}, fmt.Errorf("polymarket: stock quotes not supported")
}

// Markets searches prediction markets by query string.
// Returns active markets matching the query, sorted by volume descending.
func (f *PolymarketFeed) Markets(query string) ([]Market, error) {
	url := fmt.Sprintf("%s/markets?tag=%s&limit=10&closed=false&order=volume&asc=false",
		f.baseURL, urlEncode(query))

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("polymarket: create request: %w", err)
	}
	req.Header.Set("User-Agent", "github.com/Timwood0x10/ares/1.0")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("polymarket: fetch markets: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("http: close response body failed", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("polymarket: returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("polymarket: read body: %w", err)
	}

	// Polymarket Gamma API returns an array of market objects.
	var raw []polymarketRawMarket
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("polymarket: decode: %w", err)
	}

	markets := make([]Market, 0, len(raw))
	for _, r := range raw {
		m := Market{
			ID:         r.ID,
			Question:   r.Question,
			Volume:     parseFloatSafe(r.Volume),
			EndDate:    r.EndDate,
			Resolution: r.Resolution,
		}

		// Extract outcome prices.
		for _, o := range r.Outcomes {
			if strings.EqualFold(o.Outcome, "YES") {
				m.YesPrice = parseFloatSafe(o.Price)
			} else if strings.EqualFold(o.Outcome, "NO") {
				m.NoPrice = parseFloatSafe(o.Price)
			}
		}

		if m.Question != "" {
			markets = append(markets, m)
		}
	}

	return markets, nil
}

// SentimentSignal extracts a sentiment score from prediction market data.
// Returns the YES price (0.0-1.0) for the first relevant market, or 0.5 if none.
// A price > 0.6 indicates bullish sentiment, < 0.4 indicates bearish.
func SentimentSignal(markets []Market) float64 {
	if len(markets) == 0 {
		return 0.5
	}
	// Use the highest-volume market's YES price.
	best := markets[0]
	for _, m := range markets {
		if m.Volume > best.Volume {
			best = m
		}
	}
	return best.YesPrice
}

// ─── API Response Types ──────────────────────────────────

type polymarketRawMarket struct {
	ID         string                 `json:"id"`
	Question   string                 `json:"question"`
	Volume     string                 `json:"volume"`
	EndDate    string                 `json:"end_date"`
	Resolution string                 `json:"resolution"`
	Outcomes   []polymarketRawOutcome `json:"outcomes"`
}

type polymarketRawOutcome struct {
	Outcome string `json:"outcome"`
	Price   string `json:"price"`
}

// ─── Helpers ──────────────────────────────────────────────

func parseFloatSafe(s string) float64 {
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0
	}
	return v
}

func urlEncode(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}
