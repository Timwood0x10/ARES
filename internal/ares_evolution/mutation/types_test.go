package mutation

import (
	"strings"
	"testing"
	"time"
)

func TestMutationTypeString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		give MutationType
		want string
	}{
		{give: MutationParameter, want: "parameter"},
		{give: MutationPrompt, want: "prompt"},
		{give: MutationTool, want: "tool"},
		{give: MutationCrossover, want: "crossover"},
		{give: MutationRoot, want: "root"},
		{give: MutationType(0), want: "unknown"},
		{give: MutationType(99), want: "unknown"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.give.String(); got != tt.want {
				t.Errorf("MutationType(%d).String() = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestParseMutationType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		give    string
		want    MutationType
		wantLog bool // whether a warning log is expected (for garbage input)
	}{
		{name: "parameter", give: "parameter", want: MutationParameter, wantLog: false},
		{name: "prompt", give: "prompt", want: MutationPrompt, wantLog: false},
		{name: "tool", give: "tool", want: MutationTool, wantLog: false},
		{name: "crossover", give: "crossover", want: MutationCrossover, wantLog: false},
		{name: "root", give: "root", want: MutationRoot, wantLog: false},
		{name: "empty string treated as root", give: "", want: MutationRoot, wantLog: false},
		{name: "garbage falls back to root with warning", give: "garbage", want: MutationRoot, wantLog: true},
		{name: "unknown type falls back to root with warning", give: "random_type", want: MutationRoot, wantLog: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ParseMutationType(tt.give)
			if got != tt.want {
				t.Errorf("ParseMutationType(%q) = %d, want %d", tt.give, got, tt.want)
			}
		})
	}
}

func TestMutationRootRoundTrip(t *testing.T) {
	t.Parallel()

	if got := ParseMutationType(MutationRoot.String()); got != MutationRoot {
		t.Errorf("ParseMutationType(MutationRoot.String()) = %d, want %d", got, MutationRoot)
	}
}

// TestComputeEvidenceKey_IncludesToolsField verifies Y.3-ACT: the tool
// whitelist in Params["tools"] is included in the evidence key so two
// strategies that differ only in tool selection land on different keys.
func TestComputeEvidenceKey_IncludesToolsField(t *testing.T) {
	t.Parallel()

	base := Strategy{
		ID:             "test-evidence-tools",
		Version:        1,
		PromptTemplate: "default prompt",
		Params:         map[string]any{"temperature": 0.5},
		CreatedAt:      time.Now(),
	}

	stratA := base.Clone()
	stratA.Params["tools"] = "web_search,calculator"

	stratB := base.Clone()
	stratB.Params["tools"] = "web_search,code_exec"

	keyA := stratA.ComputeEvidenceKey()
	keyB := stratB.ComputeEvidenceKey()

	if keyA == keyB {
		t.Errorf("strategies with different tool whitelists must have different evidence keys: both got %q", keyA)
	}
}

// TestComputeEvidenceKey_ToolOrderIndependent verifies that the tool field
// in the evidence key is order-independent: "b,a" and "a, b" produce the same
// key because the set of tools is what matters, not the order.
func TestComputeEvidenceKey_ToolOrderIndependent(t *testing.T) {
	t.Parallel()

	base := Strategy{
		ID:             "test-evidence-order",
		Version:        1,
		PromptTemplate: "default prompt",
		Params:         map[string]any{"temperature": 0.5},
		CreatedAt:      time.Now(),
	}

	stratA := base.Clone()
	stratA.Params["tools"] = "web_search,calculator"

	stratB := base.Clone()
	stratB.Params["tools"] = "calculator, web_search"

	keyA := stratA.ComputeEvidenceKey()
	keyB := stratB.ComputeEvidenceKey()

	if keyA != keyB {
		t.Errorf("evidence key must be order-independent for tools: got %q and %q", keyA, keyB)
	}
}

// TestComputeEvidenceKey_NoToolsField verifies that strategies without a
// tools field produce a key without the tools suffix.
func TestComputeEvidenceKey_NoToolsField(t *testing.T) {
	t.Parallel()

	s := Strategy{
		ID:             "test-evidence-no-tools",
		Version:        1,
		PromptTemplate: "default prompt",
		Params:         map[string]any{"temperature": 0.5},
		CreatedAt:      time.Now(),
	}

	key := s.ComputeEvidenceKey()

	// Key should not contain "|tools="
	if strings.Contains(key, "|tools=") {
		t.Errorf("evidence key should not contain tools suffix when no tools field: got %q", key)
	}
}

// TestComputeEvidenceKey_EmptyToolsField verifies that an empty tools string
// does not add the tools suffix to the evidence key.
func TestComputeEvidenceKey_EmptyToolsField(t *testing.T) {
	t.Parallel()

	s := Strategy{
		ID:             "test-evidence-empty-tools",
		Version:        1,
		PromptTemplate: "default prompt",
		Params:         map[string]any{"temperature": 0.5, "tools": ""},
		CreatedAt:      time.Now(),
	}

	key := s.ComputeEvidenceKey()

	if strings.Contains(key, "|tools=") {
		t.Errorf("evidence key should not contain tools suffix for empty tools: got %q", key)
	}
}
