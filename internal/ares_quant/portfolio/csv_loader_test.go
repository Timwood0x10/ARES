package portfolio

import (
	"os"
	"path/filepath"
	"testing"
)

// ─── Test fixtures ───────────────────────────────────────────

const validSignalsCSV = `date,symbol,action,weight,quantity,reason
2025-01-02,AAPL,BUY,0.8,100,earnings beat
2025-01-03,AAPL,SELL,0.5,50,take profit
2025-01-05,TSLA,HOLD,0.3,0,wait for dip
`

const invalidActionCSV = `date,symbol,action,weight,quantity,reason
2025-01-02,AAPL,SHORT,0.8,100,invalid action
`

const negativePriceCustomCSV = `date,symbol,open,high,low,close,volume
2025-01-02,CUSTOM_A,-10.0,-9.0,-11.0,-10.5,1000
`

const validCustomAssetCSV = `date,symbol,open,high,low,close,volume
2025-01-02,CUSTOM_A,10.0,12.0,9.5,11.0,1000
2025-01-03,CUSTOM_A,11.0,13.0,10.5,12.5,1200
2025-01-02,CUSTOM_B,200.0,205.0,198.0,202.0,500
2025-01-03,CUSTOM_B,203.0,210.0,201.0,208.0,600
`

const minimalSignalsCSV = `date,symbol,action,weight
2025-06-01,TEST,BUY,0.5
`

const duplicateSignalsCSV = `date,symbol,action,weight,quantity,reason
2025-01-02,AAPL,BUY,0.8,100,first signal
2025-01-02,AAPL,SELL,0.5,50,duplicate date+symbol
`

// ─── LoadSignalsFromCSV tests ─────────────────────────────────

// TestLoadSignalsFromCSVValid verifies that a well-formed signals CSV is
// parsed correctly with all fields populated.
func TestLoadSignalsFromCSVValid(t *testing.T) {
	path := writeTestCSVFile(t, validSignalsCSV)
	signals, err := LoadSignalsFromCSV(path)
	if err != nil {
		t.Fatalf("LoadSignalsFromCSV failed: %v", err)
	}

	if len(signals) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(signals))
	}

	// Verify first signal.
	if signals[0].Action != "BUY" {
		t.Errorf("signal[0] action = %q, want BUY", signals[0].Action)
	}
	if signals[0].Confidence != 0.8 {
		t.Errorf("signal[0] confidence = %.2f, want 0.80", signals[0].Confidence)
	}
	if signals[0].Reason != "earnings beat" {
		t.Errorf("signal[0] reason = %q, want 'earnings beat'", signals[0].Reason)
	}

	// Verify second signal.
	if signals[1].Action != "SELL" {
		t.Errorf("signal[1] action = %q, want SELL", signals[1].Action)
	}

	// Verify third HOLD signal.
	if signals[2].Action != "HOLD" {
		t.Errorf("signal[2] action = %q, want HOLD", signals[2].Action)
	}
}

// TestLoadSignalsFromCSVInvalidAction verifies that an invalid action value
// causes an error.
func TestLoadSignalsFromCSVInvalidAction(t *testing.T) {
	path := writeTestCSVFile(t, invalidActionCSV)
	_, err := LoadSignalsFromCSV(path)
	if err == nil {
		t.Fatal("expected error for invalid action SHORT")
	}
	// The error message should mention "invalid action".
	if !containsStr(err.Error(), "invalid action") && !containsStr(err.Error(), "SHORT") {
		t.Errorf("error should mention invalid action or SHORT, got: %v", err)
	}
}

// TestLoadSignalsFromCSVDuplicateKey verifies that duplicate date+symbol rows
// are accepted (warning only) and all signals are returned.
func TestLoadSignalsFromCSVDuplicateKey(t *testing.T) {
	path := writeTestCSVFile(t, duplicateSignalsCSV)
	signals, err := LoadSignalsFromCSV(path)
	if err != nil {
		t.Fatalf("LoadSignalsFromCSV with duplicates should not fail: %v", err)
	}
	if len(signals) != 2 {
		t.Errorf("expected 2 signals (including duplicate), got %d", len(signals))
	}
}

// TestLoadSignalsFromCSVMinimal verifies that a CSV with only required columns
// (no quantity/reason) parses correctly.
func TestLoadSignalsFromCSVMinimal(t *testing.T) {
	path := writeTestCSVFile(t, minimalSignalsCSV)
	signals, err := LoadSignalsFromCSV(path)
	if err != nil {
		t.Fatalf("LoadSignalsFromCSV minimal failed: %v", err)
	}
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].Action != "BUY" {
		t.Errorf("signal action = %q, want BUY", signals[0].Action)
	}
}

// TestLoadSignalsFromCSVEmptyFile verifies that a header-only file returns nil.
func TestLoadSignalsFromCSVEmptyFile(t *testing.T) {
	path := writeTestCSVFile(t, "date,symbol,action,weight\n")
	signals, err := LoadSignalsFromCSV(path)
	if err == nil {
		t.Error("expected error for empty CSV file")
	}
	if signals != nil {
		t.Errorf("expected nil for header-only file, got %d signals", len(signals))
	}
}

// ─── LoadCustomBarsFromCSV tests ──────────────────────────────

// TestLoadCustomBarsFromCSVValid verifies that multi-symbol OHLCV data is
// correctly grouped by symbol.
func TestLoadCustomBarsFromCSVValid(t *testing.T) {
	path := writeTestCSVFile(t, validCustomAssetCSV)
	bars, err := LoadCustomBarsFromCSV(path)
	if err != nil {
		t.Fatalf("LoadCustomBarsFromCSV failed: %v", err)
	}

	// Should have 2 symbols.
	if len(bars) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(bars))
	}

	aBars, ok := bars["CUSTOM_A"]
	if !ok {
		t.Fatal("missing CUSTOM_A in result")
	}
	if len(aBars) != 2 {
		t.Errorf("CUSTOM_A expected 2 bars, got %d", len(aBars))
	}
	if aBars[0].Close != 11.0 {
		t.Errorf("CUSTOM_A bar[0] close = %.1f, want 11.0", aBars[0].Close)
	}

	bBars, ok := bars["CUSTOM_B"]
	if !ok {
		t.Fatal("missing CUSTOM_B in result")
	}
	if len(bBars) != 2 {
		t.Errorf("CUSTOM_B expected 2 bars, got %d", len(bBars))
	}
	if bBars[1].Close != 208.0 {
		t.Errorf("CUSTOM_B bar[1] close = %.1f, want 208.0", bBars[1].Close)
	}
}

// TestLoadCustomBarsFromCSVNegativePrice verifies that a negative price
// triggers an error.
func TestLoadCustomBarsFromCSVNegativePrice(t *testing.T) {
	path := writeTestCSVFile(t, negativePriceCustomCSV)
	_, err := LoadCustomBarsFromCSV(path)
	if err == nil {
		t.Fatal("expected error for negative open price")
	}
	if !containsStr(err.Error(), "> 0") && !containsStr(err.Error(), "-10") {
		t.Errorf("error should mention positive price requirement, got: %v", err)
	}
}

// TestLoadCustomBarsFromCSVMinimal verifies that a minimal valid custom asset
// CSV parses correctly.
func TestLoadCustomBarsFromCSVMinimal(t *testing.T) {
	minimal := `date,symbol,open,high,low,close,volume
2025-07-01,MINI,1.0,2.0,0.9,1.5,100
`
	path := writeTestCSVFile(t, minimal)
	bars, err := LoadCustomBarsFromCSV(path)
	if err != nil {
		t.Fatalf("LoadCustomBarsFromCSV minimal failed: %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(bars))
	}
	miniBars := bars["MINI"]
	if len(miniBars) != 1 || miniBars[0].Close != 1.5 {
		t.Errorf("MINI bar close = %.1f, want 1.5", miniBars[0].Close)
	}
}

// TestLoadCustomBarsFromCSVEmptyFile verifies that a header-only file returns nil.
func TestLoadCustomBarsFromCSVEmptyFile(t *testing.T) {
	path := writeTestCSVFile(t, "date,symbol,open,high,low,close,volume\n")
	bars, err := LoadCustomBarsFromCSV(path)
	if err == nil {
		t.Error("expected error for empty CSV file")
	}
	if bars != nil {
		t.Errorf("expected nil for header-only file, got %d symbols", len(bars))
	}
}

// ─── Helpers ──────────────────────────────────────────────────

func writeTestCSVFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test_data.csv")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test CSV: %v", err)
	}
	return path
}

func containsStr(s, substr string) bool { return len(s) >= len(substr) && searchString(s, substr) }

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
