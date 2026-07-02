package indicators

import (
	"math"
	"testing"
)

func near(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}

func nearSlice(got, want []float64, eps float64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !near(got[i], want[i], eps) {
			return false
		}
	}
	return true
}

func TestSMA_Normal(t *testing.T) {
	prices := []float64{1, 2, 3, 4, 5}
	got := SMA(prices, 3)
	want := []float64{2, 3, 4}
	if !nearSlice(got, want, 1e-9) {
		t.Errorf("SMA = %v, want %v", got, want)
	}
}

func TestSMA_InsufficientData(t *testing.T) {
	if got := SMA([]float64{1, 2}, 5); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestSMA_SingleElement(t *testing.T) {
	got := SMA([]float64{10}, 1)
	want := []float64{10}
	if !nearSlice(got, want, 1e-9) {
		t.Errorf("SMA = %v, want %v", got, want)
	}
}

func TestSMA_Empty(t *testing.T) {
	if got := SMA(nil, 10); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if got := SMA([]float64{}, 5); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestSMA_PeriodEqualsLength(t *testing.T) {
	prices := []float64{10, 20, 30}
	got := SMA(prices, 3)
	want := []float64{20}
	if !nearSlice(got, want, 1e-9) {
		t.Errorf("SMA = %v, want %v", got, want)
	}
}

func TestEMA_Normal(t *testing.T) {
	prices := []float64{1, 2, 3, 4, 5}
	got := EMA(prices, 3)
	if len(got) != len(prices) {
		t.Fatalf("EMA length = %d, want %d", len(got), len(prices))
	}
	// First EMA value is SMA of first 3: (1+2+3)/3 = 2
	if !near(got[2], 2.0, 1e-9) {
		t.Errorf("EMA[2] = %f, want 2.0", got[2])
	}
	// Multiplier = 2/(3+1) = 0.5
	// EMA[3] = (4 - 2) * 0.5 + 2 = 3
	if !near(got[3], 3.0, 1e-9) {
		t.Errorf("EMA[3] = %f, want 3.0", got[3])
	}
	// EMA[4] = (5 - 3) * 0.5 + 3 = 4
	if !near(got[4], 4.0, 1e-9) {
		t.Errorf("EMA[4] = %f, want 4.0", got[4])
	}
}

func TestEMA_InsufficientData(t *testing.T) {
	if got := EMA([]float64{1, 2}, 5); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestEMA_ZeroPeriod(t *testing.T) {
	if got := EMA([]float64{1, 2, 3}, 0); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	if got := EMA([]float64{1, 2, 3}, -1); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestRSI_Normal(t *testing.T) {
	// 14 days of steadily increasing prices -> RSI should be high (near 100).
	prices := make([]float64, 20)
	for i := range prices {
		prices[i] = 100 + float64(i)
	}
	got := RSI(prices, 14)
	if len(got) != len(prices)-14 {
		t.Fatalf("RSI length = %d, want %d", len(got), len(prices)-14)
	}
	// All gains, no losses -> RSI == 100
	for i, v := range got {
		if !near(v, 100, 1e-9) {
			t.Errorf("RSI[%d] = %f, want 100", i, v)
		}
	}
}

func TestRSI_AllLosses(t *testing.T) {
	prices := make([]float64, 20)
	for i := range prices {
		prices[i] = 200 - float64(i)
	}
	got := RSI(prices, 14)
	for i, v := range got {
		if !near(v, 0, 1e-9) {
			t.Errorf("RSI[%d] = %f, want 0", i, v)
		}
	}
}

func TestRSI_AllEqual(t *testing.T) {
	prices := make([]float64, 20)
	for i := range prices {
		prices[i] = 100
	}
	got := RSI(prices, 14)
	// No gains, no losses -> avgLoss = 0 -> RSI = 100
	for i, v := range got {
		if !near(v, 100, 1e-9) {
			t.Errorf("RSI[%d] = %f, want 100", i, v)
		}
	}
}

func TestRSI_Mixed(t *testing.T) {
	prices := []float64{
		44, 44.34, 44.09, 43.61, 44.33, 44.83, 45.10, 45.42, 45.84,
		46.08, 45.89, 46.03, 45.61, 46.28, 46.28, 46.00, 46.03, 46.41, 46.22, 46.21,
	}
	got := RSI(prices, 14)
	if len(got) == 0 {
		t.Fatal("RSI returned empty slice")
	}
	// RSI should be within [0,100] for mixed data.
	for _, v := range got {
		if v < 0 || v > 100 {
			t.Errorf("RSI value %f out of range [0,100]", v)
		}
	}
}

func TestRSI_InsufficientData(t *testing.T) {
	if got := RSI([]float64{1, 2, 3}, 5); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestMACD_Normal(t *testing.T) {
	prices := make([]float64, 50)
	for i := range prices {
		prices[i] = 100 + float64(i)*0.5
	}
	macdLine, signalLine, hist := MACD(prices, 12, 26, 9)
	if macdLine == nil {
		t.Fatal("MACD returned nil")
	}
	if len(macdLine) != len(prices)-26+1 {
		t.Errorf("macdLine length = %d, want %d", len(macdLine), len(prices)-26+1)
	}
	if len(signalLine) != len(macdLine) {
		t.Errorf("signalLine length = %d, want %d", len(signalLine), len(macdLine))
	}
	if len(hist) == 0 {
		t.Errorf("histogram is empty")
	}
}

func TestMACD_InsufficientData(t *testing.T) {
	m, s, h := MACD([]float64{1, 2, 3}, 12, 26, 9)
	if m != nil || s != nil || h != nil {
		t.Error("expected all nil for insufficient data")
	}
}

func TestBollingerBands_Normal(t *testing.T) {
	prices := []float64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	upper, lower, mid := BollingerBands(prices, 3, 2.0)
	if upper == nil || lower == nil || mid == nil {
		t.Fatal("BollingerBands returned nil")
	}
	if len(upper) != len(prices)-3+1 {
		t.Errorf("upper length = %d, want %d", len(upper), len(prices)-3+1)
	}
	if len(lower) != len(mid) || len(upper) != len(mid) {
		t.Error("upper/lower/mid length mismatch")
	}
	// Upper must be >= middle >= lower for every element.
	for i := range mid {
		if lower[i] > mid[i] {
			t.Errorf("lower[%d]=%f > mid[%d]=%f", i, lower[i], i, mid[i])
		}
		if mid[i] > upper[i] {
			t.Errorf("mid[%d]=%f > upper[%d]=%f", i, mid[i], i, upper[i])
		}
	}
}

func TestBollingerBands_InsufficientData(t *testing.T) {
	u, l, m := BollingerBands([]float64{1, 2}, 5, 2.0)
	if u != nil || l != nil || m != nil {
		t.Error("expected all nil for insufficient data")
	}
}

func TestSqrt(t *testing.T) {
	cases := []struct {
		input float64
		want  float64
	}{
		{0, 0},
		{1, 1},
		{4, 2},
		{9, 3},
		{2, 1.41421356},
		{100, 10},
	}
	for _, c := range cases {
		got := sqrt(c.input)
		if !near(got, c.want, 1e-4) {
			t.Errorf("sqrt(%f) = %f, want %f", c.input, got, c.want)
		}
	}
}

func TestSqrt_Negative(t *testing.T) {
	if got := sqrt(-1); got != 0 {
		t.Errorf("sqrt(-1) = %f, want 0", got)
	}
}

func TestSMA_Internal(t *testing.T) {
	got := sma([]float64{1, 2, 3, 4}, 4)
	want := 2.5
	if !near(got, want, 1e-9) {
		t.Errorf("sma = %f, want %f", got, want)
	}
}
