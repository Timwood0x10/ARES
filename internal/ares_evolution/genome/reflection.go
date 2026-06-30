package genome

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// Reflection captures LLM analysis of evolution history.
// It describes patterns, root causes, and actionable insights
// derived from examining strategy outcomes across generations.
type Reflection struct {
	// Summary is a one-sentence overview of the key finding.
	Summary string `json:"summary"`

	// Patterns lists observed behavioral or performance patterns.
	Patterns []Pattern `json:"patterns"`

	// Recommendations are concrete suggestions for the next evolution cycle.
	Recommendations []Recommendation `json:"recommendations"`

	// Confidence is the LLM's certainty in this reflection [0, 1].
	Confidence float64 `json:"confidence"`
}

// Pattern describes an observed phenomenon in strategy behavior.
type Pattern struct {
	// Description of what was observed.
	Description string `json:"description"`

	// Evidence supporting this pattern (e.g. "3/5 failed strategies had tool X").
	Evidence string `json:"evidence"`

	// Severity: "positive", "neutral", "negative"
	Severity string `json:"severity"`
}

// Recommendation is a concrete action suggested by the reflection.
type Recommendation struct {
	// Target is what to change (e.g. "param:temperature", "prompt", "tool").
	Target string `json:"target"`

	// Action is what to do (e.g. "decrease", "swap", "restructure").
	Action string `json:"action"`

	// Rationale explains why this change is recommended.
	Rationale string `json:"rationale"`

	// ExpectedImpact describes the hypothesized effect.
	ExpectedImpact string `json:"expected_impact"`

	// Confidence in this recommendation [0, 1].
	Confidence float64 `json:"confidence"`
}

// Reflector produces structured reflections from evolution history data.
type Reflector interface {
	// Reflect analyzes strategy outcomes and generates a structured reflection.
	Reflect(ctx context.Context, history []GenerationHistoryEntry, agents []*mutation.Strategy) (*Reflection, error)
}

// LLMReflector uses an LLM to analyze evolution history and produce reflections.
type LLMReflector struct {
	client LLMClient
}

// LLMClient is the interface for LLM text generation.
type LLMClient interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// NewLLMReflector creates a reflection engine backed by an LLM.
func NewLLMReflector(client LLMClient) *LLMReflector {
	return &LLMReflector{client: client}
}

// Reflect analyzes evolution history and generates a structured reflection.
func (r *LLMReflector) Reflect(ctx context.Context, history []GenerationHistoryEntry, agents []*mutation.Strategy) (*Reflection, error) {
	prompt := r.buildReflectionPrompt(history, agents)
	resp, err := r.client.Generate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM reflection failed: %w", err)
	}

	jsonStr := extractJSONBracketOuter(resp)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in LLM reflection response")
	}

	var ref Reflection
	if err := json.Unmarshal([]byte(jsonStr), &ref); err == nil && ref.Summary != "" {
		return &ref, nil
	}

	var refs []Reflection
	if err := json.Unmarshal([]byte(jsonStr), &refs); err == nil && len(refs) > 0 {
		return &refs[0], nil
	}

	return nil, fmt.Errorf("unmarshal reflection: JSON did not match Reflection structure")
}

func (r *LLMReflector) buildReflectionPrompt(history []GenerationHistoryEntry, agents []*mutation.Strategy) string {
	maxHistory := 20
	if len(history) > maxHistory {
		history = history[len(history)-maxHistory:]
	}

	var b strings.Builder
	b.WriteString("Evolution History:\n")
	for _, h := range history {
		fmt.Fprintf(&b, "  Gen %d: best=%.3f, avg=%.3f, diversity=%.3f, pop=%d\n",
			h.Generation, h.BestScore, h.AvgScore, h.Diversity, h.PopulationSize)
	}

	b.WriteString("\nCurrent Population:\n")
	maxAgents := 30
	for i, a := range agents {
		if i >= maxAgents {
			fmt.Fprintf(&b, "  ... and %d more\n", len(agents)-maxAgents)
			break
		}
		fmt.Fprintf(&b, "  ID=%s score=%.3f type=%s\n", a.ID, a.Score, a.StrategyMutationType.String())
	}

	return fmt.Sprintf(`You are an evolutionary strategy analyst. Analyze the evolution data and provide structured insights.

%s

Return a JSON object with:
- "summary": one-sentence key finding
- "patterns": array of {"description":"...","evidence":"...","severity":"positive|neutral|negative"}
- "recommendations": array of {"target":"...","action":"...","rationale":"...","expected_impact":"...","confidence":0.0-1.0}
- "confidence": overall confidence 0.0-1.0

Return ONLY valid JSON. No markdown, no explanation.`, b.String())
}

// extractJSONBracketOuter finds the outermost JSON object or array in a string.
func extractJSONBracketOuter(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, open := range []byte{'{', '['} {
		openIdx := strings.IndexByte(s, open)
		if openIdx < 0 {
			continue
		}
		var close byte
		if open == '{' {
			close = '}'
		} else {
			close = ']'
		}
		closeIdx := strings.LastIndexByte(s, close)
		if closeIdx > openIdx {
			return s[openIdx : closeIdx+1]
		}
	}
	return s
}
