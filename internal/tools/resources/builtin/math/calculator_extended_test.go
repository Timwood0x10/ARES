package builtin

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func TestCalculatorBasicArithmetic(t *testing.T) {
	c := NewCalculator()
	tests := []struct {
		expr string
		want float64
	}{
		{"1 + 2", 3},
		{"10 - 4", 6},
		{"3 * 4", 12},
		{"20 / 5", 4},
		{"2 ** 10", 1024},
		{"(1 + 2) * 3", 9},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := c.Execute(context.Background(), map[string]interface{}{"expression": tt.expr})
			if err != nil {
				t.Fatalf("Execute(%q): %v", tt.expr, err)
			}
			if !result.Success {
				t.Fatalf("Execute(%q) failed: %v", tt.expr, result.Error)
			}
			data := result.Data.(map[string]interface{})
			got := data["result"].(float64)
			if got != tt.want {
				t.Errorf("Execute(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestCalculatorFunctions(t *testing.T) {
	c := NewCalculator()
	tests := []struct {
		expr string
	}{
		{"sqrt(4)"},
		{"abs(-5)"},
		{"round(3.14159, 2)"},
		{"floor(3.7)"},
		{"ceil(3.2)"},
		{"sin(0)"},
		{"cos(0)"},
		{"log(100)"},
		{"pow(2, 3)"},
		{"min(1, 2)"},
		{"max(1, 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := c.Execute(context.Background(), map[string]interface{}{"expression": tt.expr})
			if err != nil {
				t.Fatalf("Execute(%q): %v", tt.expr, err)
			}
			if !result.Success {
				t.Errorf("Execute(%q) failed: %v", tt.expr, result.Error)
			}
		})
	}
}

func TestCalculatorConstants(t *testing.T) {
	c := NewCalculator()
	result, err := c.Execute(context.Background(), map[string]interface{}{"expression": "pi"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("Execute(pi) failed: %v", result.Error)
	}
	data := result.Data.(map[string]interface{})
	pi := data["result"].(float64)
	if pi <= 3.14 || pi >= 3.15 {
		t.Errorf("pi = %v, want ~3.14159", pi)
	}
}

func TestCalculatorInvalidExpression(t *testing.T) {
	c := NewCalculator()
	result, _ := c.Execute(context.Background(), map[string]interface{}{"expression": "invalid("})
	if result.Success {
		t.Error("should fail for invalid expression")
	}
}

func TestCalculatorNonNumericResult(t *testing.T) {
	c := NewCalculator()
	result, _ := c.Execute(context.Background(), map[string]interface{}{"expression": `"hello"`})
	if result.Success {
		t.Error("should fail for non-numeric result")
	}
}

func TestCalculatorCompileCache(t *testing.T) {
	c := NewCalculator()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		result, err := c.Execute(ctx, map[string]interface{}{"expression": "2 + 2"})
		if err != nil {
			t.Fatalf("Execute %d: %v", i, err)
		}
		if !result.Success {
			t.Errorf("Execute %d failed: %v", i, result.Error)
		}
	}
}

func TestCalculatorCombinatorics(t *testing.T) {
	c := NewCalculator()
	tests := []struct {
		expr string
		want float64
	}{
		{"factorial(5)", 120},
		{"factorial(0)", 1},
		{"nCr(10, 3)", 120},
		{"nPr(5, 2)", 20},
		{"gcd(12, 18)", 6},
		{"lcm(4, 6)", 12},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := c.Execute(context.Background(), map[string]interface{}{"expression": tt.expr})
			if err != nil {
				t.Fatalf("Execute(%q): %v", tt.expr, err)
			}
			if !result.Success {
				t.Fatalf("Execute(%q) failed: %v", tt.expr, result.Error)
			}
			data := result.Data.(map[string]interface{})
			got := data["result"].(float64)
			if got != tt.want {
				t.Errorf("Execute(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestCalculatorStatistics(t *testing.T) {
	c := NewCalculator()
	tests := []struct {
		expr string
	}{
		{"mean(1, 2, 3, 4, 5)"},
		{"median(1, 2, 3, 4, 5)"},
		{"variance(1, 2, 3, 4, 5)"},
		{"stddev(1, 2, 3, 4, 5)"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := c.Execute(context.Background(), map[string]interface{}{"expression": tt.expr})
			if err != nil {
				t.Fatalf("Execute(%q): %v", tt.expr, err)
			}
			if !result.Success {
				t.Errorf("Execute(%q) failed: %v", tt.expr, result.Error)
			}
		})
	}
}

func TestCalculatorNumberTheory(t *testing.T) {
	c := NewCalculator()
	tests := []struct {
		expr string
		want float64
	}{
		{"isPrime(7)", 1},
		{"isPrime(4)", 0},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := c.Execute(context.Background(), map[string]interface{}{"expression": tt.expr})
			if err != nil {
				t.Fatalf("Execute(%q): %v", tt.expr, err)
			}
			if !result.Success {
				t.Fatalf("Execute(%q) failed: %v", tt.expr, result.Error)
			}
			data := result.Data.(map[string]interface{})
			got := data["result"].(float64)
			if got != tt.want {
				t.Errorf("Execute(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestCalculatorProbability(t *testing.T) {
	c := NewCalculator()
	tests := []struct {
		expr string
	}{
		{"binomial(10, 3, 0.5)"},
		{"normalPdf(0, 0, 1)"},
		{"poissonPdf(3, 2)"},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			result, err := c.Execute(context.Background(), map[string]interface{}{"expression": tt.expr})
			if err != nil {
				t.Fatalf("Execute(%q): %v", tt.expr, err)
			}
			if !result.Success {
				t.Errorf("Execute(%q) failed: %v", tt.expr, result.Error)
			}
		})
	}
}

func TestCalculatorMetadata(t *testing.T) {
	c := NewCalculator()
	if c.Description() == "" {
		t.Error("Description should not be empty")
	}
	if len(c.Capabilities()) == 0 {
		t.Error("Capabilities should not be empty")
	}
	params := c.Parameters()
	if params == nil || len(params.Required) == 0 {
		t.Error("Parameters.Required should not be empty")
	}
	_ = core.CategoryCore // verify import used
}
