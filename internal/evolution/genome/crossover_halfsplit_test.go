package genome

import (
	"context"
	"strings"
	"testing"

	"goagentx/internal/evolution/mutation"
)

func TestCrossoverWithHalfSplit(t *testing.T) {
	tests := []struct {
		name        string
		a           *mutation.Strategy
		b           *mutation.Strategy
		seed        int64
		wantErr     bool
		errContains string
		checkChild  func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy)
	}{
		{
			name: "basic half-split produces combined prompt template",
			a: makeTestStrategy("parent-a", 0.8, 1, map[string]any{
				"temperature": 0.7,
			}, "ABCDEFGHIJ"), // len=10, mid=5
			b: makeTestStrategy("parent-b", 0.6, 2, map[string]any{
				"temperature": 0.3,
			}, "klmnopqrst"), // len=10
			seed:    42,
			wantErr: false,
			checkChild: func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy) {
				if child == nil {
					t.Fatal("child should not be nil")
				}
				wantPrompt := a.PromptTemplate[:5] + b.PromptTemplate[5:]
				if child.PromptTemplate != wantPrompt {
					t.Errorf("PromptTemplate = %q, want %q", child.PromptTemplate, wantPrompt)
				}
			},
		},
		{
			name:    "child has different ID from both parents",
			a:       makeTestStrategy("aaa-111", 0.5, 1, map[string]any{"temp": 0.5}, "ABCDEFGH"),
			b:       makeTestStrategy("bbb-222", 0.5, 1, map[string]any{"temp": 0.9}, "abcdefgh"),
			seed:    99,
			wantErr: false,
			checkChild: func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy) {
				if child.ID == a.ID {
					t.Error("child ID should differ from parent A")
				}
				if child.ID == b.ID {
					t.Error("child ID should differ from parent B")
				}
			},
		},
		{
			name:    "child version is greater than both parents",
			a:       makeTestStrategy("a", 0.5, 3, map[string]any{"x": 1}, "ABCDE"),
			b:       makeTestStrategy("b", 0.5, 5, map[string]any{"y": 2}, "abcde"),
			seed:    42,
			wantErr: false,
			checkChild: func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy) {
				wantVersion := maxVersion(a.Version, b.Version) + 1
				if child.Version != wantVersion {
					t.Errorf("child Version = %d, want %d", child.Version, wantVersion)
				}
				if child.Version <= a.Version {
					t.Error("child version should be > parent A version")
				}
				if child.Version <= b.Version {
					t.Error("child version should be > parent B version")
				}
			},
		},
		{
			name:        "nil parent A returns error",
			a:           nil,
			b:           makeTestStrategy("b", 0.5, 1, map[string]any{"t": 1}, "prompt"),
			wantErr:     true,
			errContains: "parent strategy must not be nil",
		},
		{
			name:        "nil parent B returns error",
			a:           makeTestStrategy("a", 0.5, 1, map[string]any{"t": 1}, "prompt"),
			b:           nil,
			wantErr:     true,
			errContains: "parent strategy must not be nil",
		},
		{
			name: "one empty prompt template falls back to score-based selection",
			a:    makeTestStrategy("a", 0.9, 1, map[string]any{"t": 1}, ""),
			b:    makeTestStrategy("b", 0.3, 1, map[string]any{"t": 2}, "has_prompt"),
			seed: 42,
			checkChild: func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy) {
				if child.PromptTemplate != "" {
					t.Errorf("expected empty PromptTemplate from empty parent A, got %q", child.PromptTemplate)
				}
			},
		},
		{
			name: "both empty prompt templates returns empty result",
			a:    makeTestStrategy("a", 0.5, 1, map[string]any{"t": 1}, ""),
			b:    makeTestStrategy("b", 0.7, 1, map[string]any{"t": 2}, ""),
			seed: 42,
			checkChild: func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy) {
				if child.PromptTemplate != "" {
					t.Errorf("expected empty PromptTemplate, got %q", child.PromptTemplate)
				}
			},
		},
		{
			name: "short prompt template length < 2 handles gracefully",
			a:    makeTestStrategy("a", 0.5, 1, map[string]any{"t": 1}, "X"),
			b:    makeTestStrategy("b", 0.5, 1, map[string]any{"t": 2}, "YZABCD"),
			seed: 42,
			checkChild: func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy) {
				if child.PromptTemplate != "XZABCD" {
					t.Errorf("short template: PromptTemplate = %q, want %q", child.PromptTemplate, "XZABCD")
				}
			},
		},
		{
			name:        "context cancellation support",
			a:           makeTestStrategy("a", 0.5, 1, map[string]any{"t": 1}, "promptA"),
			b:           makeTestStrategy("b", 0.5, 1, map[string]any{"t": 2}, "promptB"),
			wantErr:     true,
			errContains: "cancelled",
		},
		{
			name: "deterministic results with same seed and params",
			a: makeTestStrategy("a", 0.5, 1, map[string]any{
				"p1": 10, "p2": 20, "p3": 30,
			}, "determ_prompt_AAAA"),
			b: makeTestStrategy("b", 0.5, 1, map[string]any{
				"p1": 100, "p2": 200, "p3": 300,
			}, "determ_prompt_BBBB"),
			seed:    888,
			wantErr: false,
			checkChild: func(t *testing.T, child *mutation.Strategy, a, b *mutation.Strategy) {
				c2, _ := NewCrossover(WithSeed(888))
				child2, err := c2.CrossoverWithHalfSplit(context.Background(), a, b)
				if err != nil {
					t.Fatalf("second run error: %v", err)
				}
				if child.PromptTemplate != child2.PromptTemplate {
					t.Errorf("prompt differs between runs: %q vs %q", child.PromptTemplate, child2.PromptTemplate)
				}
				for k := range child.Params {
					if child.Params[k] != child2.Params[k] {
						t.Errorf("param %q differs: %v vs %v", k, child.Params[k], child2.Params[k])
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c *Crossover
			var err error

			if tt.seed != 0 {
				c, err = NewCrossover(WithSeed(tt.seed))
			} else {
				c, err = NewCrossover()
			}
			if err != nil {
				t.Fatalf("NewCrossover error = %v", err)
			}

			ctx := context.Background()
			if tt.wantErr && tt.errContains == "cancelled" {
				cancelCtx, cancel := context.WithCancel(context.Background())
				cancel()
				ctx = cancelCtx
			}

			child, err := c.CrossoverWithHalfSplit(ctx, tt.a, tt.b)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.checkChild != nil {
				tt.checkChild(t, child, tt.a, tt.b)
			}
		})
	}
}

func TestHalfSplitPromptCrossover(t *testing.T) {
	c, _ := NewCrossover()

	tests := []struct {
		name         string
		promptA      string
		promptB      string
		scoreA       float64
		scoreB       float64
		wantPrompt   string
		wantFallback bool
	}{
		{
			name:       "even-length template correct split at midpoint",
			promptA:    "ABCDEFGH",
			promptB:    "abcdefgh",
			wantPrompt: "ABCDefgh",
		},
		{
			name:       "odd-length template floor division split",
			promptA:    "ABCDE",
			promptB:    "abcde",
			wantPrompt: "ABcde",
		},
		{
			name:         "empty template A falls back",
			promptA:      "",
			promptB:      "has_content",
			scoreA:       0.9,
			scoreB:       0.3,
			wantFallback: true,
		},
		{
			name:         "empty template B falls back",
			promptA:      "has_content",
			promptB:      "",
			scoreA:       0.3,
			scoreB:       0.9,
			wantFallback: true,
		},
		{
			name:         "both empty templates fall back",
			promptA:      "",
			promptB:      "",
			scoreA:       0.5,
			scoreB:       0.5,
			wantFallback: true,
		},
		{
			name:       "template A shorter than midpoint edge case",
			promptA:    "AB",
			promptB:    "x",
			wantPrompt: "Ax",
		},
		{
			name:       "unicode multi-byte content in templates",
			promptA:    "你好世界Hello",
			promptB:    "世界你好World",
			wantPrompt: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := makeTestStrategy("a", tt.scoreA, 1, map[string]any{}, tt.promptA)
			b := makeTestStrategy("b", tt.scoreB, 1, map[string]any{}, tt.promptB)

			got := c.halfSplitPromptCrossover(a, b)

			if tt.wantFallback {
				expected := c.selectPromptTemplate(a, b)
				if got != expected {
					t.Errorf("fallback: got %q, want %q (selectPromptTemplate result)", got, expected)
				}
				return
			}

			if tt.wantPrompt == "" && tt.promptA == "你好世界Hello" {
				runesA := []rune(tt.promptA)
				runesB := []rune(tt.promptB)
				mid := len(runesA) / 2
				if len(runesA) > 0 && mid == 0 {
					mid = 1
				}
				if len(runesB) <= mid {
					tt.wantPrompt = string(runesA[:mid]) + tt.promptB
				} else {
					tt.wantPrompt = string(runesA[:mid]) + string(runesB[mid:])
				}
			}

			if got != tt.wantPrompt {
				t.Errorf("halfSplitPromptCrossover() = %q, want %q", got, tt.wantPrompt)
			}
		})
	}
}

func TestFormatParentIDsWithUnicode(t *testing.T) {
	tests := []struct {
		idA  string
		idB  string
		want string
	}{
		{"aaa", "bbb", "aaa\u00d7bbb"},
		{"uuid-1111", "uuid-2222", "uuid-1111\u00d7uuid-2222"},
		{"", "empty-id", "\u00d7empty-id"},
		{"id-with-dashes", "id_with_underscores", "id-with-dashes\u00d7id_with_underscores"},
	}

	for _, tt := range tests {
		t.Run(tt.idA+"_"+tt.idB, func(t *testing.T) {
			got := formatParentIDs(tt.idA, tt.idB)
			if got != tt.want {
				t.Errorf("formatParentIDs(%q, %q) = %q, want %q", tt.idA, tt.idB, got, tt.want)
			}
			if strings.Contains(got, "+") {
				t.Error("ParentID should contain \u00d7, not +")
			}
			if !strings.Contains(got, "\u00d7") {
				t.Error("ParentID should contain \u00d7 (multiplication sign)")
			}
		})
	}
}

func TestHalfSplitCrossoverInterface(t *testing.T) {
	t.Run("satisfies interface", func(t *testing.T) {
		c, err := NewCrossover(WithSeed(42))
		if err != nil {
			t.Fatalf("NewCrossover error = %v", err)
		}

		var _ HalfSplitCrossoverInterface = c

		a := makeTestStrategy("a", 0.8, 1, map[string]any{"t": 0.7}, "promptAAAAAA")
		b := makeTestStrategy("b", 0.4, 1, map[string]any{"t": 0.3}, "promptBBBBBB")

		child, err := c.CrossoverWithHalfSplit(context.Background(), a, b)
		if err != nil {
			t.Fatalf("CrossoverWithHalfSplit error = %v", err)
		}
		if child == nil {
			t.Fatal("child should not be nil via HalfSplitCrossoverInterface")
		}
		if !strings.Contains(child.MutationDesc, "half_split_prompt") {
			t.Errorf("MutationDesc should contain half_split_prompt, got %q", child.MutationDesc)
		}
	})
}
