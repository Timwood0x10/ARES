package builtin

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// Calculator performs mathematical calculations using the expr library.
// Supports: +, -, *, /, %, **, parentheses, and 15+ built-in functions.
type Calculator struct {
	*base.BaseTool
	compiled map[string]*vm.Program
}

// NewCalculator creates a new Calculator tool backed by the expr evaluation engine.
//
// Supported operators: +, -, *, /, %, ** (power)
// Supported functions: sqrt, abs, sin, cos, tan, log, ln, round, floor, ceil, pow, min, max
// Supported constants: pi, e
func NewCalculator() *Calculator {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"expression": {
				Type:        "string",
				Description: "A mathematical expression to evaluate. Examples: 'sqrt(2)', '3**4', 'pi * 5^2', 'abs(-10)', 'sin(pi/2)', 'round(3.14159, 2)'",
			},
		},
		Required: []string{"expression"},
	}

	return &Calculator{
		BaseTool: base.NewBaseToolWithCapabilities("calculator",
			"Evaluate mathematical expressions. Supports Gaussian sum formulas for fast 1..n summation.\n\n"+
				"IMPORTANT FORMULAS:\n"+
				"- Sum from 1 to n: n*(n+1)/2\n"+
				"- Sum from a to b: (b-a+1)*(a+b)/2\n\n"+
				"OPERATORS:\n"+
				"- Arithmetic: +, -, *, /, % (modulo)\n"+
				"- Power: ** (e.g., 2**10 = 1024)\n"+
				"- Parentheses: ()\n\n"+
				"BASIC FUNCTIONS:\n"+
				"- sqrt(x), abs(x), round(x, n), floor(x), ceil(x)\n"+
				"- sin(x), cos(x), tan(x) — x in radians\n"+
				"- log(x), ln(x) — log base 10 and natural log\n"+
				"- pow(x, y), min(a, b), max(a, b)\n\n"+
				"COMBINATORICS:\n"+
				"- factorial(n) — n! for n >= 0\n"+
				"- nPr(n, r) — permutations: n!/(n-r)!\n"+
				"- nCr(n, r) — combinations: n!/(r!(n-r)!)\n\n"+
				"NUMBER THEORY:\n"+
				"- gcd(a, b), lcm(a, b) — greatest common divisor / least common multiple\n"+
				"- isPrime(n) — returns 1 if prime, 0 otherwise\n\n"+
				"STATISTICS:\n"+
				"- mean(a, b, c, ...), median(a, b, c, ...)\n"+
				"- variance(a, b, c, ...), stddev(a, b, c, ...)\n\n"+
				"PROBABILITY:\n"+
				"- binomial(n, k, p) — binomial probability P(X=k)\n"+
				"- normalPdf(x, mu, sigma) — normal distribution PDF\n"+
				"- poissonPdf(k, lambda) — Poisson probability P(X=k)\n\n"+
				"CONSTANTS:\n"+
				"- pi (3.14159...), e (2.71828...)\n\n"+
				"EXAMPLES:\n"+
				"- Sum 1 to 100 → 100*(100+1)/2 = 5050\n"+
				"- Sum 1 to 1000000 → 1000000*(1000000+1)/2\n"+
				"- sqrt(2), 3**4, pi * 5**2, sin(pi/2)\n"+
				"- factorial(5), nCr(10, 3), gcd(12, 18)\n"+
				"- mean(1,2,3,4,5), stddev(1,2,3,4,5)\n"+
				"- binomial(10, 3, 0.5), normalPdf(0, 0, 1)",
			core.CategoryCore, []core.Capability{core.CapabilityMath}, params),
		compiled: make(map[string]*vm.Program),
	}
}

// Execute evaluates the expression using the expr library.
func (t *Calculator) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	expression, ok := params["expression"].(string)
	if !ok || expression == "" {
		return core.NewErrorResult("expression is required"), nil
	}

	env := map[string]interface{}{
		"pi": math.Pi,
		"e":  math.E,
	}

	// Use compiled program cache for repeated expressions.
	program, ok := t.compiled[expression]
	if !ok {
		var err error
		program, err = expr.Compile(expression, expr.Env(env),
			expr.Function("sqrt", func(params ...interface{}) (interface{}, error) {
				return math.Sqrt(toFloat64(params[0])), nil
			}),
			expr.Function("abs", func(params ...interface{}) (interface{}, error) {
				return math.Abs(toFloat64(params[0])), nil
			}),
			expr.Function("sin", func(params ...interface{}) (interface{}, error) {
				return math.Sin(toFloat64(params[0])), nil
			}),
			expr.Function("cos", func(params ...interface{}) (interface{}, error) {
				return math.Cos(toFloat64(params[0])), nil
			}),
			expr.Function("tan", func(params ...interface{}) (interface{}, error) {
				return math.Tan(toFloat64(params[0])), nil
			}),
			expr.Function("log", func(params ...interface{}) (interface{}, error) {
				return math.Log10(toFloat64(params[0])), nil
			}),
			expr.Function("ln", func(params ...interface{}) (interface{}, error) {
				return math.Log(toFloat64(params[0])), nil
			}),
			expr.Function("round", func(params ...interface{}) (interface{}, error) {
				if len(params) == 2 {
					return math.Round(toFloat64(params[0])*math.Pow10(int(toFloat64(params[1])))) / math.Pow10(int(toFloat64(params[1]))), nil
				}
				return math.Round(toFloat64(params[0])), nil
			}),
			expr.Function("floor", func(params ...interface{}) (interface{}, error) {
				return math.Floor(toFloat64(params[0])), nil
			}),
			expr.Function("ceil", func(params ...interface{}) (interface{}, error) {
				return math.Ceil(toFloat64(params[0])), nil
			}),
			expr.Function("pow", func(params ...interface{}) (interface{}, error) {
				return math.Pow(toFloat64(params[0]), toFloat64(params[1])), nil
			}),
			expr.Function("min", func(params ...interface{}) (interface{}, error) {
				return math.Min(toFloat64(params[0]), toFloat64(params[1])), nil
			}),
			expr.Function("max", func(params ...interface{}) (interface{}, error) {
				return math.Max(toFloat64(params[0]), toFloat64(params[1])), nil
			}),
			// ── Combinatorics ──
			expr.Function("factorial", func(params ...interface{}) (interface{}, error) {
				n := int(toFloat64(params[0]))
				if n < 0 {
					return nil, fmt.Errorf("factorial: n must be >= 0")
				}
				result := 1.0
				for i := 2; i <= n; i++ {
					result *= float64(i)
				}
				return result, nil
			}),
			expr.Function("nPr", func(params ...interface{}) (interface{}, error) {
				n := int(toFloat64(params[0]))
				r := int(toFloat64(params[1]))
				if n < 0 || r < 0 || r > n {
					return nil, fmt.Errorf("nPr: invalid arguments")
				}
				result := 1.0
				for i := n; i > n-r; i-- {
					result *= float64(i)
				}
				return result, nil
			}),
			expr.Function("nCr", func(params ...interface{}) (interface{}, error) {
				n := int(toFloat64(params[0]))
				r := int(toFloat64(params[1]))
				if n < 0 || r < 0 || r > n {
					return nil, fmt.Errorf("nCr: invalid arguments")
				}
				if r == 0 || r == n {
					return 1.0, nil
				}
				r = minInt(r, n-r)
				result := 1.0
				for i := 1; i <= r; i++ {
					result = result * float64(n-r+i) / float64(i)
				}
				return result, nil
			}),
			// ── Number Theory ──
			expr.Function("gcd", func(params ...interface{}) (interface{}, error) {
				a, b := int(toFloat64(params[0])), int(toFloat64(params[1]))
				a, b = absInt(a), absInt(b)
				for b != 0 {
					a, b = b, a%b
				}
				return float64(a), nil
			}),
			expr.Function("lcm", func(params ...interface{}) (interface{}, error) {
				a, b := int(toFloat64(params[0])), int(toFloat64(params[1]))
				if a == 0 || b == 0 {
					return 0.0, nil
				}
				g := a
				for g != 0 {
					a, b = b, a%b
					g = a
				}
				// Now a is gcd
				gcd := a
				if gcd == 0 {
					gcd = 1
				}
				return float64(absInt(int(toFloat64(params[0])) * int(toFloat64(params[1])) / gcd)), nil
			}),
			expr.Function("isPrime", func(params ...interface{}) (interface{}, error) {
				n := int(toFloat64(params[0]))
				if n < 2 {
					return 0.0, nil
				}
				for i := 2; i*i <= n; i++ {
					if n%i == 0 {
						return 0.0, nil
					}
				}
				return 1.0, nil
			}),
			// ── Statistics ──
			expr.Function("mean", func(params ...interface{}) (interface{}, error) {
				if len(params) == 0 {
					return nil, fmt.Errorf("mean: no arguments")
				}
				sum := 0.0
				for _, p := range params {
					sum += toFloat64(p)
				}
				return sum / float64(len(params)), nil
			}),
			expr.Function("variance", func(params ...interface{}) (interface{}, error) {
				if len(params) < 2 {
					return nil, fmt.Errorf("variance: need at least 2 values")
				}
				avg := 0.0
				for _, p := range params {
					avg += toFloat64(p)
				}
				avg /= float64(len(params))
				sumSq := 0.0
				for _, p := range params {
					d := toFloat64(p) - avg
					sumSq += d * d
				}
				return sumSq / float64(len(params)), nil
			}),
			expr.Function("stddev", func(params ...interface{}) (interface{}, error) {
				if len(params) < 2 {
					return nil, fmt.Errorf("stddev: need at least 2 values")
				}
				avg := 0.0
				for _, p := range params {
					avg += toFloat64(p)
				}
				avg /= float64(len(params))
				sumSq := 0.0
				for _, p := range params {
					d := toFloat64(p) - avg
					sumSq += d * d
				}
				return math.Sqrt(sumSq / float64(len(params))), nil
			}),
			expr.Function("median", func(params ...interface{}) (interface{}, error) {
				if len(params) == 0 {
					return nil, fmt.Errorf("median: no arguments")
				}
				vals := make([]float64, len(params))
				for i, p := range params {
					vals[i] = toFloat64(p)
				}
				sort.Float64s(vals)
				n := len(vals)
				if n%2 == 0 {
					return (vals[n/2-1] + vals[n/2]) / 2, nil
				}
				return vals[n/2], nil
			}),
			// ── Probability ──
			expr.Function("binomial", func(params ...interface{}) (interface{}, error) {
				n := int(toFloat64(params[0]))
				k := int(toFloat64(params[1]))
				p := toFloat64(params[2])
				if n < 0 || k < 0 || k > n || p < 0 || p > 1 {
					return nil, fmt.Errorf("binomial: invalid arguments")
				}
				// nCk * p^k * (1-p)^(n-k)
				coeff := 1.0
				kr := minInt(k, n-k)
				for i := 1; i <= kr; i++ {
					coeff = coeff * float64(n-kr+i) / float64(i)
				}
				return coeff * math.Pow(p, float64(k)) * math.Pow(1-p, float64(n-k)), nil
			}),
			expr.Function("normalPdf", func(params ...interface{}) (interface{}, error) {
				x := toFloat64(params[0])
				mu := toFloat64(params[1])
				sigma := toFloat64(params[2])
				if sigma <= 0 {
					return nil, fmt.Errorf("normalPdf: sigma must be > 0")
				}
				return (1 / (sigma * math.Sqrt(2*math.Pi))) *
					math.Exp(-((x-mu)*(x-mu))/(2*sigma*sigma)), nil
			}),
			expr.Function("poissonPdf", func(params ...interface{}) (interface{}, error) {
				k := int(toFloat64(params[0]))
				lambda := toFloat64(params[1])
				if k < 0 || lambda <= 0 {
					return nil, fmt.Errorf("poissonPdf: k >= 0, lambda > 0")
				}
				// λ^k * e^(-λ) / k!
				logFact := 0.0
				for i := 2; i <= k; i++ {
					logFact += math.Log(float64(i))
				}
				return math.Exp(float64(k)*math.Log(lambda) - lambda - logFact), nil
			}),
		)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("invalid expression: %v", err)), nil
		}
		t.compiled[expression] = program
	}

	output, err := expr.Run(program, env)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("evaluation error: %v", err)), nil
	}

	result, ok := toFloat64Safe(output)
	if !ok {
		return core.NewErrorResult("expression did not return a number"), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"expression": expression,
		"result":     result,
	}), nil
}

// IsIdempotent returns true since calculator has no side effects.
func (t *Calculator) IsIdempotent() bool { return true }

// toFloat64 converts any numeric value to float64.
func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return 0
	}
}

// toFloat64Safe safely converts a value to float64, returning false on failure.
func toFloat64Safe(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}

// absInt returns the absolute value of an integer.
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// minInt returns the smaller of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// DateTime provides date and time operations.
type DateTime struct {
	*base.BaseTool
}

// NewDateTime creates a new DateTime tool.
func NewDateTime() *DateTime {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (now, format, parse, add, diff)",
				Enum:        []interface{}{"now", "format", "parse", "add", "diff"},
			},
			"time_string": {
				Type:        "string",
				Description: "Time string for parse/format operations",
			},
			"format": {
				Type:        "string",
				Description: "Format string (e.g., '2006-01-02 15:04:05')",
			},
			"duration": {
				Type:        "string",
				Description: "Duration to add (e.g., '1h', '30m', '2d')",
			},
		},
		Required: []string{"operation"},
	}

	return &DateTime{
		BaseTool: base.NewBaseToolWithCapabilities("datetime", "Get current time and perform date/time operations", core.CategoryCore, []core.Capability{core.CapabilityTime}, params),
	}
}

// Execute performs the date/time operation.
func (t *DateTime) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	now := time.Now()

	switch operation {
	case "now":
		format := getString(params, "format")
		if format == "" {
			format = "2006-01-02 15:04:05"
		}
		return core.NewResult(true, map[string]interface{}{
			"formatted": now.Format(format),
			"unix":      now.Unix(),
			"unix_nano": now.UnixNano(),
		}), nil

	case "format":
		timeStr := getString(params, "time_string")
		if timeStr == "" {
			return core.NewErrorResult("time_string is required for format operation"), nil
		}
		format := getString(params, "format")
		if format == "" {
			format = "2006-01-02 15:04:05"
		}
		parsedTime, err := time.Parse(format, timeStr)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse time: %v", err)), nil
		}
		return core.NewResult(true, map[string]interface{}{
			"parsed":    parsedTime,
			"unix":      parsedTime.Unix(),
			"formatted": parsedTime.Format(format),
		}), nil

	case "parse":
		timeStr := getString(params, "time_string")
		if timeStr == "" {
			return core.NewErrorResult("time_string is required for parse operation"), nil
		}
		// Try common formats
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
			"2006/01/02 15:04:05",
			"2006/01/02",
		}
		var parsedTime time.Time
		var err error
		for _, format := range formats {
			parsedTime, err = time.Parse(format, timeStr)
			if err == nil {
				break
			}
		}
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse time with common formats: %v", err)), nil
		}
		return core.NewResult(true, map[string]interface{}{
			"parsed": parsedTime,
			"unix":   parsedTime.Unix(),
		}), nil

	case "add":
		durationStr := getString(params, "duration")
		if durationStr == "" {
			return core.NewErrorResult("duration is required for add operation"), nil
		}
		duration, err := time.ParseDuration(durationStr)
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse duration: %v", err)), nil
		}
		result := now.Add(duration)
		return core.NewResult(true, map[string]interface{}{
			"original": now,
			"duration": durationStr,
			"result":   result,
			"unix":     result.Unix(),
		}), nil

	case "diff":
		timeStr := getString(params, "time_string")
		if timeStr == "" {
			return core.NewErrorResult("time_string is required for diff operation"), nil
		}
		// Parse time
		var parsedTime time.Time
		var err error
		formats := []string{
			time.RFC3339,
			"2006-01-02 15:04:05",
			"2006-01-02",
		}
		for _, format := range formats {
			parsedTime, err = time.Parse(format, timeStr)
			if err == nil {
				break
			}
		}
		if err != nil {
			return core.NewErrorResult(fmt.Sprintf("failed to parse time: %v", err)), nil
		}
		diff := now.Sub(parsedTime)
		return core.NewResult(true, map[string]interface{}{
			"now":      now,
			"target":   parsedTime,
			"duration": diff,
			"seconds":  diff.Seconds(),
			"minutes":  diff.Minutes(),
			"hours":    diff.Hours(),
			"days":     diff.Hours() / 24,
		}), nil

	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

func (t *DateTime) IsIdempotent() bool { return true }

// TextProcessor provides text processing operations.
type TextProcessor struct {
	*base.BaseTool
}

// NewTextProcessor creates a new TextProcessor tool.
func NewTextProcessor() *TextProcessor {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (count, split, replace, uppercase, lowercase, trim, contains)",
				Enum:        []interface{}{"count", "split", "replace", "uppercase", "lowercase", "trim", "contains"},
			},
			"text": {
				Type:        "string",
				Description: "Text to process",
			},
			"separator": {
				Type:        "string",
				Description: "Separator for split operation",
			},
			"old": {
				Type:        "string",
				Description: "Old substring to replace",
			},
			"new": {
				Type:        "string",
				Description: "New substring",
			},
			"substring": {
				Type:        "string",
				Description: "Substring to check for contains operation",
			},
		},
		Required: []string{"operation", "text"},
	}

	return &TextProcessor{
		BaseTool: base.NewBaseToolWithCapabilities("text_processor", "Perform text processing operations", core.CategoryCore, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the text processing operation.
func (t *TextProcessor) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	text, ok := params["text"].(string)
	if !ok {
		return core.NewErrorResult("text is required"), nil
	}

	switch operation {
	case "count":
		return core.NewResult(true, map[string]interface{}{
			"length": len(text),
			"chars":  len([]rune(text)),
			"words":  len(strings.Fields(text)),
			"lines":  len(strings.Split(text, "\n")),
		}), nil

	case "split":
		separator := getString(params, "separator")
		if separator == "" {
			separator = " "
		}
		parts := strings.Split(text, separator)
		return core.NewResult(true, map[string]interface{}{
			"parts": parts,
			"count": len(parts),
		}), nil

	case "replace":
		oldStr := getString(params, "old")
		newStr := getString(params, "new")
		if oldStr == "" {
			return core.NewErrorResult("old substring is required for replace operation"), nil
		}
		result := strings.ReplaceAll(text, oldStr, newStr)
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   result,
		}), nil

	case "uppercase":
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   strings.ToUpper(text),
		}), nil

	case "lowercase":
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   strings.ToLower(text),
		}), nil

	case "trim":
		return core.NewResult(true, map[string]interface{}{
			"original": text,
			"result":   strings.TrimSpace(text),
		}), nil

	case "contains":
		substring := getString(params, "substring")
		if substring == "" {
			return core.NewErrorResult("substring is required for contains operation"), nil
		}
		contains := strings.Contains(text, substring)
		return core.NewResult(true, map[string]interface{}{
			"contains": contains,
		}), nil

	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

func (t *TextProcessor) IsIdempotent() bool { return true }

// getString safely gets a string parameter.
func getString(params map[string]interface{}, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}
