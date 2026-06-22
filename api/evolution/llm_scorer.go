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

// Default deterministic scorer constants.
const (
	// deterministicBaseScore is the starting score before parameter adjustments.
	deterministicBaseScore = 50.0

	// deterministicMaxScore is the upper clamp limit for the deterministic scorer.
	deterministicMaxScore = 100.0

	// deterministicMinScore is the lower clamp limit for the deterministic scorer.
	deterministicMinScore = 5.0

	// deterministicPromptReward is the bonus for "precise" prompt template.
	deterministicPromptReward = 15.0

	// deterministicCarefulReward is the bonus for "careful" prompt template.
	deterministicCarefulReward = 8.0

	// deterministicCreativeReward is the bonus for "creative" prompt template.
	deterministicCreativeReward = 4.0
)

// DeterministicScore computes a parameter-aware fitness score for a strategy.
// This is a pure heuristic (no random noise / no LLM call) used as fallback
// scorer when no custom LLM scorer is configured. The scoring formula:
//
//   - Base score: 50.0
//   - temperature: lower is better → (1.0 - temp) * 25  (range [0, +25])
//   - top_k near 30 is optimal → penalty = dist²/10 where dist = top_k - 30
//   - prompt template: "precise" +15, "careful" +8, "creative" +4
//   - Final score clamped to [deterministicMinScore, deterministicMaxScore]
//
// Args:
//
//	s - the strategy to score (must not be nil).
//
// Returns:
//
//	float64 - fitness score in [deterministicMinScore, deterministicMaxScore].
func DeterministicScore(s *Strategy) float64 {
	if s == nil {
		return deterministicBaseScore
	}

	score := deterministicBaseScore

	// Temperature: lower is better (0.0 -> +25, 1.0 -> +0).
	if temp, ok := s.Params["temperature"].(float64); ok {
		score += (1.0 - temp) * 25
	}

	// Top_k: optimal near 30. Penalty is quadratic distance from optimum.
	if tk, ok := s.Params["top_k"].(float64); ok {
		dist := tk - 30.0
		score -= (dist * dist) / 10.0
	}

	// Prompt template bonus.
	promptVal := ""
	if pt, ok := s.Params["prompt_template"].(string); ok {
		promptVal = pt
	} else if pt, ok := s.Params["PromptTemplate"].(string); ok {
		promptVal = pt
	} else if s.PromptTemplate != "" {
		promptVal = s.PromptTemplate
	}
	switch promptVal {
	case "precise":
		score += deterministicPromptReward
	case "careful":
		score += deterministicCarefulReward
	case "creative":
		score += deterministicCreativeReward
	}

	// Clamp to valid range.
	if score > deterministicMaxScore {
		score = deterministicMaxScore
	}
	if score < deterministicMinScore {
		score = deterministicMinScore
	}
	return score
}

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
	// When 0, defaults to 0.3. When Seed != 0, forced to 0 for deterministic output.
	Temperature float64

	// MaxTokens for the LLM response.
	MaxTokens int

	// Seed enables deterministic LLM scoring when > 0.
	// Forces Temperature to 0 and embeds the seed in the evaluation prompt
	// so identical strategies always receive the same score.
	Seed int64

	// NumSamples sets how many times the LLM is called per strategy.
	// When > 1, the best score across all samples is returned (max-of-N),
	// hedging against transient API errors. Default 1 (single call).
	NumSamples int

	// Fallback is called when the LLM API is unreachable (all samples fail).
	// When set, the evolution keeps running with deterministic scoring instead
	// of assigning zeros that would collapse the population.
	// Example: pass a parameter-aware ScorerFunc as the circuit breaker.
	Fallback ScorerFunc
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
	seed        int64
	numSamples  int
	fallback    ScorerFunc
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
	forcedDeterministic := false
	if cfg.Seed != 0 {
		temp = 0 // seed requires deterministic output
		forcedDeterministic = true
	}
	if temp == 0 && !forcedDeterministic {
		temp = defaultLLMTemperature
	}
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultLLMMaxTokens
	}
	numSamples := cfg.NumSamples
	if numSamples < 1 {
		numSamples = 1
	}
	return &LLMScorer{
		client:      cfg.Client,
		evalPrompt:  evalPrompt,
		model:       cfg.Model,
		temperature: temp,
		maxTokens:   maxTokens,
		seed:        cfg.Seed,
		numSamples:  numSamples,
		fallback:    cfg.Fallback,
	}, nil
}

// Score implements scorer for population evaluation.
// It builds a prompt from the strategy, calls the LLM, and parses the score.
// When numSamples > 1, the LLM is called multiple times and the best score
// is returned (max-of-N), providing robustness against transient API failures
// and non-deterministic outputs. Max is used instead of median because the
// primary noise source is API errors (score=0), not numerical variance —
// a single successful call gives the best available LLM judgment.
func (s *LLMScorer) Score(strategy *Strategy) float64 {
	return s.ScoreWithContext(context.Background(), strategy)
}

// ScoreWithContext evaluates a strategy using the LLM with a provided context.
// This is the context-aware variant of Score, useful when the caller has an
// active request context for cancellation or timeout control.
func (s *LLMScorer) ScoreWithContext(ctx context.Context, strategy *Strategy) float64 {
	if strategy == nil {
		return 0
	}

	if s.numSamples <= 1 {
		return s.sampleOnce(ctx, strategy)
	}

	best := 0.0
	for range s.numSamples {
		sc := s.sampleOnce(ctx, strategy)
		if sc > best {
			best = sc
		}
	}
	return best
}

// sampleOnce calls the LLM once for the given strategy and returns the parsed score.
// If the LLM call fails and a fallback scorer is configured, the fallback is used
// instead — this keeps the evolution running when the API is temporarily down.
func (s *LLMScorer) sampleOnce(ctx context.Context, strategy *Strategy) float64 {
	prompt := s.buildPrompt(strategy)
	resp, err := s.client.Generate(ctx, prompt)
	if err != nil {
		if s.fallback != nil {
			return s.fallback(strategy)
		}
		return 0
	}
	return s.parseScore(resp)
}

// AsScorerFunc returns a ScorerFunc that delegates to this LLMScorer.
// Note: the returned function uses context.Background() since ScorerFunc
// does not carry context. Callers that need context propagation should
// use ScoreWithContext directly.
func (s *LLMScorer) AsScorerFunc() ScorerFunc {
	return func(agent *Strategy) float64 {
		return s.ScoreWithContext(context.Background(), agent)
	}
}

// buildPrompt constructs the evaluation prompt from a strategy.
// When a seed is configured, it embeds a determinism instruction to reduce
// output variance across repeated evaluations of the same parameters.
func (s *LLMScorer) buildPrompt(strategy *Strategy) string {
	params := make(map[string]any)
	for k, v := range strategy.Params {
		params[k] = v
	}
	params["prompt_template"] = strategy.PromptTemplate
	params["name"] = strategy.Name

	data, _ := json.MarshalIndent(params, "  ", "  ")
	prompt := strings.ReplaceAll(s.evalPrompt, "{strategy_json}", string(data))

	if s.seed != 0 {
		prompt += fmt.Sprintf("\n\n(Scoring seed: %d. Use temperature 0 for fully deterministic evaluation.)", s.seed)
	}
	return prompt
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
