package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	defaultLLMTemperature = 0.3
	defaultLLMMaxTokens   = 256
)

// LLMScorerConfig configures an LLMScorer instance.
type LLMScorerConfig struct {
	// Client is the LLM client used for scoring (must not be nil).
	Client LLMClient

	// EvalPrompt is the evaluation prompt template.
	// The strategy params and prompt_template are interpolated.
	// If empty, a default prompt is used.
	EvalPrompt string

	// Model is the LLM model name (for logging/metrics).
	Model string

	// Temperature for the LLM evaluation call.
	Temperature float64

	// MaxTokens for the LLM response.
	MaxTokens int
}

// DefaultEvalPrompt is the default evaluation prompt template.
// {strategy_json} is replaced with the strategy's JSON representation.
const DefaultEvalPrompt = `You are evaluating an AI agent strategy. The strategy defines how an agent:
- Generates responses (temperature controls creativity vs determinism)
- Selects knowledge (top_k controls focus breadth)
- Structures its prompts (prompt_template sets behavior style)

Score this strategy on a scale of 0 to 100 based on:
- Reasoning quality: Does the temperature setting allow coherent reasoning?
- Focus accuracy: Does top_k balance breadth vs precision?
- Instruction following: Does the prompt template guide appropriate behavior?
- General capability: Would this strategy perform well on diverse tasks?

Strategy configuration:
{strategy_json}

Respond with ONLY a JSON object containing:
{"score": <0-100>, "reasoning": "<brief explanation>", "focus": "<brief explanation>", "instruction": "<brief explanation>"}`

// LLMScorer evaluates strategies by calling an LLM.
// It serializes the strategy config, sends it to the LLM,
// and extracts a score from the structured response.
type LLMScorer struct {
	client      LLMClient
	evalPrompt  string
	model       string
	temperature float64
	maxTokens   int
}

// NewLLMScorer creates an LLMScorer from config.
func NewLLMScorer(cfg LLMScorerConfig) (*LLMScorer, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("LLM client must not be nil")
	}
	evalPrompt := cfg.EvalPrompt
	if evalPrompt == "" {
		evalPrompt = DefaultEvalPrompt
	}
	temp := cfg.Temperature
	if temp == 0 {
		temp = defaultLLMTemperature
	}
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultLLMMaxTokens
	}
	return &LLMScorer{
		client:      cfg.Client,
		evalPrompt:  evalPrompt,
		model:       cfg.Model,
		temperature: temp,
		maxTokens:   maxTokens,
	}, nil
}

// Score implements scorer for population evaluation.
// It builds a prompt from the strategy, calls the LLM, and parses the score.
func (s *LLMScorer) Score(strategy *Strategy) float64 {
	if strategy == nil {
		return 0
	}

	prompt := s.buildPrompt(strategy)
	resp, err := s.client.Generate(context.Background(), prompt)
	if err != nil {
		return 0
	}

	return s.parseScore(resp)
}

// AsScorerFunc returns a ScorerFunc that delegates to this LLMScorer.
func (s *LLMScorer) AsScorerFunc() ScorerFunc {
	return func(agent *Strategy) float64 {
		return s.Score(agent)
	}
}

// buildPrompt constructs the evaluation prompt from a strategy.
func (s *LLMScorer) buildPrompt(strategy *Strategy) string {
	params := make(map[string]any)
	for k, v := range strategy.Params {
		params[k] = v
	}
	params["prompt_template"] = strategy.PromptTemplate
	params["name"] = strategy.Name

	data, _ := json.MarshalIndent(params, "  ", "  ")
	return strings.ReplaceAll(s.evalPrompt, "{strategy_json}", string(data))
}

// parseScore extracts a numeric score from the LLM response.
// Expects a JSON response with a "score" field.
// Falls back to rule-based estimation if parsing fails.
func (s *LLMScorer) parseScore(resp string) float64 {
	resp = strings.TrimSpace(resp)

	var parsed struct {
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err == nil && parsed.Score > 0 {
		if parsed.Score > 100 {
			return 100
		}
		return parsed.Score
	}

	// Fallback: extract score via keyword heuristic.
	return s.fallbackScore(resp)
}

// fallbackScore estimates quality from the LLM's free-text response.
func (s *LLMScorer) fallbackScore(resp string) float64 {
	keywords := map[string]float64{
		"excellent": 90, "outstanding": 95, "very good": 80,
		"good": 70, "decent": 60, "average": 50,
		"poor": 30, "bad": 20, "terrible": 10,
	}
	lower := strings.ToLower(resp)
	best := 50.0
	for kw, score := range keywords {
		if strings.Contains(lower, kw) && score > best {
			best = score
		}
	}
	return best
}
