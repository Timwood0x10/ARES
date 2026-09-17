// detector_imperative_test.go locks REVIEW 3.3#7: the negative-keyword
// check runs BEFORE the problem-keyword check, so imperative request openers
// ("show me", "tell me", "what is this", "what's happening") in the negative
// list silently rejected common instruction-style questions that carry
// problem keywords. Pre-fix, "show me how to fix the timeout error" was
// classified as a non-problem.
package distillation

import "testing"

func TestIsProblemImperativeQuestions(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "show me with problem keywords",
			text:     "Show me how to fix the timeout error",
			expected: true,
		},
		{
			name:     "tell me with problem keywords",
			text:     "Tell me why the build fails with a panic",
			expected: true,
		},
		{
			name:     "tell me how-to question",
			text:     "tell me how to debug this crash?",
			expected: true,
		},
		{
			name:     "what is this question mark",
			text:     "What is this error?",
			expected: true,
		},
		{
			name:     "what's happening question mark",
			text:     "What's happening with the deployment?",
			expected: true,
		},
		// Acknowledgments must remain non-problems.
		{
			name:     "plain show me without question signal",
			text:     "show me the dashboard",
			expected: false,
		},
		{
			name:     "acknowledgment still rejected",
			text:     "got it, thanks",
			expected: false,
		},
		{
			name:     "thanks still rejected",
			text:     "thanks for the help",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsProblem(tt.text); got != tt.expected {
				t.Errorf("IsProblem(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}
