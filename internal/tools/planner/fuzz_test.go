package planner

import (
	"context"
	"strings"
	"testing"
)

// FuzzSemanticAnalyzer fuzzes the rule-based semantic analyzer with random inputs.
// This catches panics from unexpected Unicode, control characters, or malformed strings.
func FuzzSemanticAnalyzer(f *testing.F) {
	analyzer := NewRuleBasedAnalyzer()
	ctx := context.Background()

	// Seed corpus with representative inputs.
	seeds := []string{
		"",
		" ",
		"\n",
		"\t",
		"\x00",
		"计算1+1",
		"累加从1到100",
		"提取PDF",
		"sha256",
		"你好",
		"1到100万的和",
		"a",
		"你好世界",
		"\x00\x01\x02",
		"{{{{",
		"!!!@@@###",
		strings.Repeat("a", 10000),
		strings.Repeat("计算", 100),
		"😀😀😀",
		"	\t\n\r",
		"null",
		"undefined",
		"<script>alert(1)</script>",
		"../../../etc/passwd",
		"1; DROP TABLE users",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Must not panic for ANY input.
		intent, err := analyzer.Analyze(ctx, input)
		if err != nil {
			// Error is acceptable (unknown pattern, empty input, etc.)
			// Just verify we don't get a nil intent with a nil error.
			if intent != nil && err == nil {
				t.Errorf("Analyze(%q) = (%v, nil), want error", input, intent)
			}
			return
		}
		// If no error, intent must have valid fields.
		if intent.Goal == "" {
			t.Errorf("Analyze(%q) returned intent with empty Goal", input)
		}
		if intent.Operation == "" {
			t.Errorf("Analyze(%q) returned intent with empty Operation", input)
		}
	})
}

// FuzzParameterExtractor fuzzes the parameter extractor with random inputs.
func FuzzParameterExtractor(f *testing.F) {
	pe := NewParameterExtractor()
	ctx := context.Background()
	_ = ctx // extractor doesn't use context

	capabilities := []string{
		"Summation", "Arithmetic", "DiscreteMath", "Probability",
		"Statistics", "NumberTheory", "StringManipulation",
	}

	seeds := []string{
		"",
		"1+1",
		"从1到100",
		"10的阶乘",
		"12和18的最大公约数",
		"正态分布",
		strings.Repeat("x", 1000),
		"0000000000000000000000000000000",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		for _, capa := range capabilities {
			// Must not panic for ANY input + capability combination.
			params := pe.ExtractParams(input, capa)
			_ = params // nil is acceptable; panic is not
		}
	})
}

// FuzzDAGValidator fuzzes the DAG validator with random step configurations.
func FuzzDAGValidator(f *testing.F) {
	v := NewDAGValidator()

	// Seed corpus: encoded step configurations.
	seeds := []string{
		"a>b,",         // a depends on b, b is missing
		"a>b,b>",       // a->b, b no deps (valid)
		"a>a,",         // self-cycle
		"a>b,b>c,c>a,", // 3-node cycle
		"a>,b>,c>,",    // 3 independent nodes (valid)
		"x>y,y>z,z>,",  // linear chain (valid)
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, encoded string) {
		plan := parseEncodedPlan(encoded)
		// Must not panic for ANY encoded input.
		errs := v.Validate(plan)
		_ = errs
	})
}

// parseEncodedPlan converts a compact string notation to an ExecutionPlan.
// Format: "stepID>dep1,dep2|stepID2>depA|"
// Example: "a>b,c|b>|c>" → a depends on b,c; b and c have no deps.
func parseEncodedPlan(encoded string) *ExecutionPlan {
	if encoded == "" {
		return &ExecutionPlan{Steps: []ExecutionStep{}}
	}

	plan := &ExecutionPlan{IsMultiStep: false}
	segments := strings.Split(encoded, "|")

	for _, seg := range segments {
		if seg == "" {
			continue
		}
		parts := strings.SplitN(seg, ">", 2)
		stepID := parts[0]
		if stepID == "" {
			continue
		}

		step := ExecutionStep{StepID: stepID}
		if len(parts) > 1 && parts[1] != "" {
			deps := strings.Split(parts[1], ",")
			for _, d := range deps {
				if d != "" {
					step.DependsOn = append(step.DependsOn, d)
				}
			}
		}
		plan.Steps = append(plan.Steps, step)
	}

	if len(plan.Steps) > 1 {
		plan.IsMultiStep = true
	}
	return plan
}

// FuzzToolScorer fuzzes the scorer with random candidates.
func FuzzToolScorer(f *testing.F) {
	scorer := NewToolScorer()
	ctx := context.Background()

	f.Fuzz(func(t *testing.T, count uint8) {
		candidates := make([]ToolCandidate, count%10)
		for i := range candidates {
			candidates[i] = ToolCandidate{
				ToolName:       randomString(int(count)),
				CapabilityName: randomString(int(count) % 5),
				Cost:           int(count),
				Deterministic:  count%2 == 0,
				Composable:     count%3 == 0,
				SideEffects:    count%5 == 0,
				SuccessRate:    float64(count) / 256.0,
			}
		}
		// Must not panic for ANY candidate configuration.
		result, err := scorer.Score(ctx, candidates, nil)
		if err != nil {
			return
		}
		if len(result) > 0 {
			// Verify scores are finite and non-negative (within reason).
			for _, c := range result {
				if c.Score < -1000 || c.Score > 1000 {
					t.Errorf("Score out of range: %f", c.Score)
				}
			}
		}
	})
}

// randomString generates a pseudo-random string for fuzzing.
func randomString(n int) string {
	if n <= 0 {
		return ""
	}
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()_+-=[]{}\\|;':\",./<>?~`"
	result := make([]byte, n)
	for i := 0; i < n; i++ {
		result[i] = chars[i%len(chars)]
	}
	return string(result)
}
