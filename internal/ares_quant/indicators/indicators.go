// Package indicators provides technical analysis computations.
// All functions are pure Go, no external dependencies.
package indicators

// MACD computes Moving Average Convergence Divergence.
// Returns (macdLine, signalLine, histogram) slices of length len(prices).
func MACD(prices []float64, fast, slow, signal int) ([]float64, []float64, []float64) {
	if len(prices) < slow+signal {
		return nil, nil, nil
	}

	emaFast := EMA(prices, fast)
	emaSlow := EMA(prices, slow)

	// MACD line = emaFast - emaSlow (aligned from index slow-1).
	macdLine := make([]float64, len(prices)-slow+1)
	for i := range macdLine {
		macdLine[i] = emaFast[i+slow-fast] - emaSlow[i]
	}

	// Signal line = EMA of macdLine.
	signalLine := EMA(macdLine, signal)

	// Histogram = macdLine - signalLine (aligned).
	histLen := len(macdLine) - signal + 1
	if histLen < 0 {
		histLen = 0
	}
	histogram := make([]float64, histLen)
	offset := len(macdLine) - histLen
	for i := range histogram {
		histogram[i] = macdLine[i+offset] - signalLine[i]
	}

	return macdLine, signalLine, histogram
}

// RSI computes Relative Strength Index over the given period.
// Returns a slice of length len(prices)-period.
func RSI(prices []float64, period int) []float64 {
	if len(prices) < period+1 {
		return nil
	}

	gains := make([]float64, len(prices)-1)
	losses := make([]float64, len(prices)-1)
	for i := 1; i < len(prices); i++ {
		diff := prices[i] - prices[i-1]
		if diff > 0 {
			gains[i-1] = diff
		} else {
			losses[i-1] = -diff
		}
	}

	avgGain := sma(gains[:period], period)
	avgLoss := sma(losses[:period], period)

	result := make([]float64, len(prices)-period)
	for i := range result {
		if i == 0 {
			if avgLoss == 0 {
				result[i] = 100
			} else {
				rs := avgGain / avgLoss
				result[i] = 100 - (100 / (1 + rs))
			}
			continue
		}
		idx := period + i - 1
		avgGain = ((avgGain * float64(period-1)) + gains[idx]) / float64(period)
		avgLoss = ((avgLoss * float64(period-1)) + losses[idx]) / float64(period)
		if avgLoss == 0 {
			result[i] = 100
		} else {
			rs := avgGain / avgLoss
			result[i] = 100 - (100 / (1 + rs))
		}
	}
	return result
}

// SMA computes Simple Moving Average over the given period.
// Returns a slice of length len(prices)-period+1.
func SMA(prices []float64, period int) []float64 {
	if len(prices) < period {
		return nil
	}
	result := make([]float64, len(prices)-period+1)
	for i := range result {
		var sum float64
		for j := 0; j < period; j++ {
			sum += prices[i+j]
		}
		result[i] = sum / float64(period)
	}
	return result
}

// EMA computes Exponential Moving Average.
// Returns a slice of length len(prices).
func EMA(prices []float64, period int) []float64 {
	if len(prices) < period || period <= 0 {
		return nil
	}
	multiplier := 2.0 / float64(period+1)
	result := make([]float64, len(prices))
	// First EMA = SMA of first period values.
	var sum float64
	for i := 0; i < period; i++ {
		sum += prices[i]
	}
	result[period-1] = sum / float64(period)
	// Remaining: EMA = (price - prevEMA) * multiplier + prevEMA.
	for i := period; i < len(prices); i++ {
		result[i] = (prices[i]-result[i-1])*multiplier + result[i-1]
	}
	return result
}

// BollingerBands computes Bollinger Bands.
// Returns (upper, lower, middle) bands.
func BollingerBands(prices []float64, period int, stdDev float64) ([]float64, []float64, []float64) {
	mid := SMA(prices, period)
	if mid == nil {
		return nil, nil, nil
	}

	upper := make([]float64, len(mid))
	lower := make([]float64, len(mid))

	for i := range mid {
		idx := i // SMA aligns to index 'period-1+i'
		var sumSq float64
		count := 0
		for j := 0; j < period && idx+j < len(prices); j++ {
			diff := prices[idx+j] - mid[i]
			sumSq += diff * diff
			count++
		}
		std := 0.0
		if count > 0 {
			std = sqrt(sumSq / float64(count))
		}
		upper[i] = mid[i] + stdDev*std
		lower[i] = mid[i] - stdDev*std
	}
	return upper, lower, mid
}

// ─── Internal Helpers ──────────────────────────────────

func sma(values []float64, period int) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(period)
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Newton's method for sqrt.
	z := x / 2
	for i := 0; i < 10; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
