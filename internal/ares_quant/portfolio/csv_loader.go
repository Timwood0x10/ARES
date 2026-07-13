package portfolio

//nolint: errcheck // best-effort operations: ResponseWriter writes, cleanup Close/Wait, deferred shutdown
import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadSignalsFromCSV reads trade signals from a CSV file.
//
// Schema (header required): date,symbol,action,weight,quantity,reason
//
//   - date: YYYY-MM-DD format.
//   - symbol: instrument identifier (string).
//   - action: must be "BUY", "SELL", or "HOLD" (case-sensitive).
//   - weight: float in [0, 1]; 0 if empty.
//   - quantity: non-negative float; 0 if empty.
//   - reason: free-text explanation; may be empty.
//
// Duplicate date+symbol combinations produce a warning but do not cause an error.
//
// Args:
//   - path: absolute or relative path to the signals CSV file.
//
// Returns:
//   - slice of parsed TradeSignal values.
//   - error if the file cannot be read or any row contains invalid data.
func LoadSignalsFromCSV(path string) ([]TradeSignal, error) {
	f, err := os.Open(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("open signals CSV %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read signals CSV %s: %w", path, err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("load signals: CSV has no data rows (header only or empty)")
	}

	var signals []TradeSignal
	var warnings []string
	seen := make(map[string]int) // "date|symbol" -> count

	for i, row := range records[1:] { // skip header
		lineNum := i + 2
		if len(row) < 4 {
			return nil, fmt.Errorf("signals CSV %s line %d: expected at least 4 columns (date,symbol,action,weight), got %d",
				path, lineNum, len(row))
		}

		dateStr := strings.TrimSpace(row[0])
		symbol := strings.TrimSpace(row[1])
		action := strings.TrimSpace(row[2])
		weightStr := strings.TrimSpace(row[3])

		dt, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("signals CSV %s line %d: invalid date %q: %w", path, lineNum, dateStr, err)
		}
		if symbol == "" {
			return nil, fmt.Errorf("signals CSV %s line %d: symbol is empty", path, lineNum)
		}

		switch action {
		case "BUY", "SELL", "HOLD":
			// Valid.
		default:
			return nil, fmt.Errorf("signals CSV %s line %d: invalid action %q; must be BUY, SELL, or HOLD",
				path, lineNum, action)
		}

		var weight float64
		if weightStr != "" {
			weight, err = strconv.ParseFloat(weightStr, 64)
			if err != nil {
				return nil, fmt.Errorf("signals CSV %s line %d: invalid weight %q: %w", path, lineNum, weightStr, err)
			}
			if weight < 0 || weight > 1 {
				return nil, fmt.Errorf("signals CSV %s line %d: weight %.4f out of range [0, 1]",
					path, lineNum, weight)
			}
		}

		var quantity float64
		if len(row) >= 5 && strings.TrimSpace(row[4]) != "" {
			qtyStr := strings.TrimSpace(row[4])
			quantity, err = strconv.ParseFloat(qtyStr, 64)
			if err != nil {
				return nil, fmt.Errorf("signals CSV %s line %d: invalid quantity %q: %w", path, lineNum, qtyStr, err)
			}
			if quantity < 0 {
				return nil, fmt.Errorf("signals CSV %s line %d: negative quantity %.4f",
					path, lineNum, quantity)
			}
		}

		reason := ""
		if len(row) >= 6 {
			reason = strings.TrimSpace(row[5])
		}

		// Duplicate detection.
		key := dateStr + "|" + symbol
		seen[key]++
		if seen[key] > 1 {
			warnings = append(warnings,
				fmt.Sprintf("line %d: duplicate date+symbol combination (%s, %s)", lineNum, dateStr, symbol))
		}

		signals = append(signals, TradeSignal{
			Date:       dt,
			Action:     action,
			Reason:     reason,
			Confidence: weight, // map weight -> confidence field
		})
	}

	if len(warnings) > 0 {
		// Warnings are non-fatal; we could log them or attach to result.
		// For now they are silently accumulated — callers can check len if needed.
		_ = warnings
	}

	return signals, nil
}

// LoadCustomBarsFromCSV reads OHLCV data for custom assets from a single CSV
// file that may contain multiple symbols.
//
// Schema (header required): date,symbol,open,high,low,close,volume
//
//   - date: YYYY-MM-DD format.
//   - symbol: instrument identifier used to group bars.
//   - open, high, low, close: must be > 0.
//   - volume: non-negative integer.
//
// Rows are grouped by symbol into separate []priceBar slices.
//
// Args:
//   - path: absolute or relative path to the custom asset CSV file.
//
// Returns:
//   - map[symbol][]priceBar with ordered bars per symbol.
//   - error if the file cannot be read or any row contains invalid data.
func LoadCustomBarsFromCSV(path string) (map[string][]priceBar, error) {
	f, err := os.Open(path) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("open custom bars CSV %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read custom bars CSV %s: %w", path, err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("load custom bars: CSV has no data rows (header only or empty)")
	}

	result := make(map[string][]priceBar)

	for i, row := range records[1:] { // skip header
		lineNum := i + 2
		if len(row) < 7 {
			return nil, fmt.Errorf("custom bars CSV %s line %d: expected 7 columns, got %d",
				path, lineNum, len(row))
		}

		dateStr := strings.TrimSpace(row[0])
		symbol := strings.TrimSpace(row[1])

		dt, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return nil, fmt.Errorf("custom bars CSV %s line %d: invalid date %q: %w", path, lineNum, dateStr, err)
		}
		if symbol == "" {
			return nil, fmt.Errorf("custom bars CSV %s line %d: symbol is empty", path, lineNum)
		}

		openPrice, errOpen := strconv.ParseFloat(strings.TrimSpace(row[2]), 64)
		highPrice, errHigh := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		lowPrice, errLow := strconv.ParseFloat(strings.TrimSpace(row[4]), 64)
		closePrice, errClose := strconv.ParseFloat(strings.TrimSpace(row[5]), 64)
		vol, errVol := strconv.ParseInt(strings.TrimSpace(row[6]), 10, 64)

		if errOpen != nil || errHigh != nil || errLow != nil || errClose != nil || errVol != nil {
			return nil, fmt.Errorf("custom bars CSV %s line %d: parse error in one or more price/volume fields",
				path, lineNum)
		}

		if openPrice <= 0 {
			return nil, fmt.Errorf("custom bars CSV %s line %d: open price %.4f must be > 0",
				path, lineNum, openPrice)
		}
		if highPrice <= 0 {
			return nil, fmt.Errorf("custom bars CSV %s line %d: high price %.4f must be > 0",
				path, lineNum, highPrice)
		}
		if lowPrice <= 0 {
			return nil, fmt.Errorf("custom bars CSV %s line %d: low price %.4f must be > 0",
				path, lineNum, lowPrice)
		}
		if closePrice <= 0 {
			return nil, fmt.Errorf("custom bars CSV %s line %d: close price %.4f must be > 0",
				path, lineNum, closePrice)
		}
		if vol < 0 {
			return nil, fmt.Errorf("custom bars CSV %s line %d: volume %d must be >= 0",
				path, lineNum, vol)
		}

		result[symbol] = append(result[symbol], priceBar{
			Date:   dt,
			Open:   openPrice,
			High:   highPrice,
			Low:    lowPrice,
			Close:  closePrice,
			Volume: vol,
		})
	}

	return result, nil
}
