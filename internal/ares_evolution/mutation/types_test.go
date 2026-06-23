package mutation

import (
	"testing"
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
