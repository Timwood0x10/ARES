package planner

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Precompiled regex patterns for parameter extraction.
// These are compiled once at package initialization to avoid
// recompilation on every ExtractParams call.
var (
	reArithmeticRange = regexp.MustCompile(`(?:从|from)\s*(\d+(?:\.\d+)?)\s*(?:到|to)\s*(\d+(?:\.\d+)?)`)
	rePower           = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*的\s*(\d+(?:\.\d+)?)\s*次方`)
	reSqrt            = regexp.MustCompile(`根号\s*(\d+(?:\.\d+)?)`)
	reSquare          = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*的\s*平方`)
	reCube            = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*的\s*立方`)
	reExprCheck       = regexp.MustCompile(`^[\d+\-*/().%\^,\s]+$`)
	reEquals          = regexp.MustCompile(`^(.+?)等于`)
	rePower2          = regexp.MustCompile(`(\d+)\s*的\s*(\d+)\s*次方`)

	reNPr        = regexp.MustCompile(`(\d+).*?(\d+).*?(?:排列|perm|nPr)`)
	reNCr        = regexp.MustCompile(`(\d+).*?(\d+).*?(?:组合|选|comb|nCr)`)
	reFactorial  = regexp.MustCompile(`(\d+)\s*(?:的阶乘|!|factorial)`)
	reBinomial   = regexp.MustCompile(`(\d+).*?(\d+).*?(\d+\.?\d*)`)
	reBinomialFn = regexp.MustCompile(`binomial\((\d+),(\d+),([\d.]+)\)`)
	reStatValues = regexp.MustCompile(`([\d.,\s]+)`)
	reTwoNums    = regexp.MustCompile(`(\d+).*?(\d+)`)
	reOneNum     = regexp.MustCompile(`(\d+)`)
)

// ParameterExtractor extracts structured parameters from natural language
// requests based on the detected capability. This enables the planner to
// fill in tool parameters without requiring an LLM.
type ParameterExtractor struct{}

// NewParameterExtractor creates a parameter extractor.
func NewParameterExtractor() *ParameterExtractor {
	return &ParameterExtractor{}
}

// ExtractParams parses a user request and returns parameters for the given
// capability. Returns nil if no parameters can be extracted (planner will
// use default empty params).
func (pe *ParameterExtractor) ExtractParams(request string, capability string) map[string]interface{} {
	switch capability {
	case "Summation", "Arithmetic":
		return pe.extractArithmetic(request)
	case "DiscreteMath":
		return pe.extractDiscreteMath(request)
	case "Probability":
		return pe.extractProbability(request)
	case "Statistics":
		return pe.extractStatistics(request)
	case "NumberTheory":
		return pe.extractNumberTheory(request)
	case "StringManipulation":
		return pe.extractStringOp(request)
	default:
		return nil
	}
}

// extractArithmetic handles basic math + summation expressions.
func (pe *ParameterExtractor) extractArithmetic(request string) map[string]interface{} {
	request = strings.TrimSpace(request)
	expr := pe.tryExtractMathExpr(request)
	return map[string]interface{}{"expression": expr}
}

// tryExtractMathExpr tries to convert natural language to an expression.
func (pe *ParameterExtractor) tryExtractMathExpr(request string) string {
	// Pattern 1: "从1累加到100" or "sum from 1 to 100"
	if m := reArithmeticRange.FindStringSubmatch(request); len(m) == 3 {
		a, _ := strconv.ParseFloat(m[1], 64)
		b, _ := strconv.ParseFloat(m[2], 64)
		if a > 0 && b > a {
			return fmt.Sprintf("%v*(%v+1)/2", b, b)
		}
	}

	// Pattern 2: "2的10次方" or "x的y次方"
	if m := rePower.FindStringSubmatch(request); len(m) == 3 {
		return fmt.Sprintf("%s**%s", m[1], m[2])
	}

	// Pattern 3: "根号16" or "sqrt 16"
	if m := reSqrt.FindStringSubmatch(request); len(m) == 2 {
		return fmt.Sprintf("sqrt(%s)", m[1])
	}

	// Pattern 4: "3的平方" (square)
	if m := reSquare.FindStringSubmatch(request); len(m) == 2 {
		return fmt.Sprintf("%s**2", m[1])
	}

	// Pattern 5: "3的立方" (cube)
	if m := reCube.FindStringSubmatch(request); len(m) == 2 {
		return fmt.Sprintf("%s**3", m[1])
	}

	// Pattern 6: Pure equation like "1+1", "sqrt(16)", "3*4+2"
	clean := strings.NewReplacer(
		"×", "*", "÷", "/", "＝", "=", "＝", "=",
		"（", "(", "）", ")", "。", "",
	).Replace(request)
	clean = strings.TrimSpace(clean)

	// Check if already looks like an expression
	if reExprCheck.MatchString(clean) {
		// Replace ^ with **
		clean = strings.ReplaceAll(clean, "^", "**")
		if clean != "" {
			return clean
		}
	}

	// Pattern 7: "1+1等于多少" or "sqrt(16)等于多少"
	if m := reEquals.FindStringSubmatch(request); len(m) == 2 {
		expr := strings.TrimSpace(m[1])
		return expr
	}

	// Pattern 8: "计算2的10次方" — extract number and power
	if m := rePower2.FindStringSubmatch(request); len(m) == 3 {
		return fmt.Sprintf("%s**%s", m[1], m[2])
	}

	// Pattern 9: "计算1+1" or "算1+1" — strip operator prefix and try as expression
	cleaned := strings.TrimPrefix(request, "计算")
	cleaned = strings.TrimPrefix(cleaned, "算")
	cleaned = strings.TrimPrefix(cleaned, "运算")
	if cleaned != request {
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			return cleaned
		}
	}

	return ""
}

// extractDiscreteMath extracts combinatorics parameters.
func (pe *ParameterExtractor) extractDiscreteMath(request string) map[string]interface{} {
	// nPr pattern: "从5个中选3个排列" or "permutation of 5 take 3"
	if m := reNPr.FindStringSubmatch(strings.ToLower(request)); len(m) == 3 {
		return map[string]interface{}{"expression": fmt.Sprintf("nPr(%s,%s)", m[1], m[2])}
	}

	// nCr pattern: "从10个中选3个组合" or "combination of 10 take 3"
	if m := reNCr.FindStringSubmatch(strings.ToLower(request)); len(m) == 3 {
		return map[string]interface{}{"expression": fmt.Sprintf("nCr(%s,%s)", m[1], m[2])}
	}

	// factorial: "10的阶乘" or "factorial of 10"
	if m := reFactorial.FindStringSubmatch(strings.ToLower(request)); len(m) == 2 {
		return map[string]interface{}{"expression": fmt.Sprintf("factorial(%s)", m[1])}
	}

	return nil
}

// extractProbability extracts probability parameters.
func (pe *ParameterExtractor) extractProbability(request string) map[string]interface{} {
	// binomial: "10次试验成功3次概率0.5" or "binomial(10,3,0.5)"
	if m := reBinomial.FindStringSubmatch(request); len(m) == 4 {
		return map[string]interface{}{"expression": fmt.Sprintf("binomial(%s,%s,%s)", m[1], m[2], m[3])}
	}

	if m := reBinomialFn.FindStringSubmatch(request); len(m) == 4 {
		return map[string]interface{}{"expression": m[0]}
	}

	return nil
}

// extractStatistics extracts stats parameters (comma-separated values).
func (pe *ParameterExtractor) extractStatistics(request string) map[string]interface{} {
	// Extract comma-separated numbers from request.
	if m := reStatValues.FindStringSubmatch(request); len(m) > 1 {
		parts := strings.FieldsFunc(m[1], func(r rune) bool {
			return r == ',' || r == '，'
		})
		if len(parts) >= 2 {
			var nums []string
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if _, err := strconv.ParseFloat(p, 64); err == nil {
					nums = append(nums, p)
				}
			}
			if len(nums) >= 2 {
				return map[string]interface{}{
					"expression": fmt.Sprintf("%s(%s)", pe.detectStatOp(request), strings.Join(nums, ",")),
				}
			}
		}
	}
	return nil
}

// detectStatOp detects which stat operation is requested.
func (pe *ParameterExtractor) detectStatOp(request string) string {
	lower := strings.ToLower(request)
	if strings.Contains(lower, "median") || strings.Contains(lower, "中位数") {
		return "median"
	}
	if strings.Contains(lower, "stddev") || strings.Contains(lower, "标准差") {
		return "stddev"
	}
	if strings.Contains(lower, "variance") || strings.Contains(lower, "方差") {
		return "variance"
	}
	return "mean"
}

// extractNumberTheory extracts number theory parameters.
func (pe *ParameterExtractor) extractNumberTheory(request string) map[string]interface{} {
	if m := reTwoNums.FindStringSubmatch(request); len(m) == 3 {
		lower := strings.ToLower(request)
		op := "gcd"
		if strings.Contains(lower, "lcm") || strings.Contains(lower, "公倍数") {
			op = "lcm"
		}
		return map[string]interface{}{"expression": fmt.Sprintf("%s(%s,%s)", op, m[1], m[2])}
	}
	// isPrime
	if m := reOneNum.FindStringSubmatch(request); len(m) == 2 {
		return map[string]interface{}{"expression": fmt.Sprintf("isPrime(%s)", m[1])}
	}
	return nil
}

// extractStringOp extracts string operation parameters.
func (pe *ParameterExtractor) extractStringOp(request string) map[string]interface{} {
	lower := strings.ToLower(request)
	op := "upper"
	switch {
	case strings.Contains(lower, "lower") || strings.Contains(lower, "小写"):
		op = "lower"
	case strings.Contains(lower, "trim") || strings.Contains(lower, "去掉"):
		op = "trim"
	case strings.Contains(lower, "reverse") || strings.Contains(lower, "反转"):
		op = "reverse"
	case strings.Contains(lower, "length") || strings.Contains(lower, "长度"):
		op = "length"
	}
	return map[string]interface{}{"operation": op}
}

// math.Round is used for rounding operations.
var _ = math.Round
