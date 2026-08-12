package evolution

import (
	"fmt"

	ares_config "github.com/Timwood0x10/ares/internal/ares_config"
	evosvc "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	"github.com/Timwood0x10/ares/internal/llm"
)

// BuildRegressionGate3 wires the candidate gate-3 regression check:
//
//	llm client -> LLMArenaScorer -> CandidateRegressionChecker
//
// and returns a check function suitable for
// CandidateVerifier.WithRegressionCheck. This is the top-level injection point
// for the preserved-case regression gate: the caller supplies an LLM client
// (real llm.Client or a test mock) and the preserved case suite.
// Args:
//
//	profileStore - reads the stable instructions of the target role; non-nil.
//	client - LLM client driving the scorer; non-nil.
//	testCases - the preserved case suite; may be empty to skip the gate.
//	opts - regression checker options (runs / min win rate / timeout).
//
// Returns:
//
//	check - the gate-3 check function (nil-check safe) for WithRegressionCheck.
//	err - when the scorer or checker cannot be built.
func BuildRegressionGate3(
	profileStore *ProfileStore,
	client evosvc.LLMClient,
	testCases []any,
	opts ...CandidateRegressionOption,
) (func(c *Candidate) error, error) {
	if client == nil {
		return nil, fmt.Errorf("gate3: llm client must not be nil")
	}
	scorer, err := evosvc.NewLLMArenaScorer(evosvc.LLMArenaScorerConfig{Client: client})
	if err != nil {
		return nil, fmt.Errorf("gate3: build arena scorer: %w", err)
	}
	checker, err := NewCandidateRegressionChecker(profileStore, scorer, testCases, opts...)
	if err != nil {
		return nil, fmt.Errorf("gate3: build regression checker: %w", err)
	}
	return checker.Check, nil
}

// LoadRegressionGate3 loads LLM clients from a YAML config file (e.g.
// configs/ares.local.yaml), then builds the gate-3 regression check exactly like
// BuildRegressionGate3. This is the convenient path for real deployments; tests
// should use BuildRegressionGate3 with a mock client instead of hitting the API.
//
// When the config's llm.fallbacks list is non-empty, the client is built as a
// FailoverClient (primary + fallbacks, e.g. agnes primary with a sensenova
// fallback): a failed or rate-limited provider automatically switches to the
// next one, so a single provider's quota exhaustion does not fail the whole
// preserved-case regression.
//
// Args:
//
//	profileStore - reads stable instructions; non-nil.
//	configPath - path to the ares YAML config with a populated llm: block.
//	testCases - the preserved case suite.
//	opts - regression checker options.
//
// Returns:
//
//	check - the gate-3 check function.
//	err - on config load, client build, or checker build failure.
func LoadRegressionGate3(
	profileStore *ProfileStore,
	configPath string,
	testCases []any,
	opts ...CandidateRegressionOption,
) (func(c *Candidate) error, error) {
	cfg, err := ares_config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("gate3: load config %q: %w", configPath, err)
	}

	var client evosvc.LLMClient
	var enabled bool
	if len(cfg.LLM.Fallbacks) > 0 {
		fc, err := llm.NewFailoverClient(toLLMConfigs(cfg.LLM), 0, 0, 0)
		if err != nil {
			return nil, fmt.Errorf("gate3: build failover llm client: %w", err)
		}
		client = fc
		// At least one provider in the chain must be usable; FailoverClient
		// switches to the next provider on failure.
		for _, c := range fc.Clients() {
			enabled = enabled || c.IsEnabled()
		}
	} else {
		c, err := llm.NewClient(toLLMConfig(&cfg.LLM))
		if err != nil {
			return nil, fmt.Errorf("gate3: build llm client: %w", err)
		}
		client = c
		enabled = c.IsEnabled()
	}

	// llm.IsEnabled() validates credentials per provider: it requires an API
	// key for openai/openrouter/anthropic but not for ollama (local, keyless).
	if !enabled {
		return nil, fmt.Errorf(
			"gate3: llm is not enabled for provider %q (missing api key in %q?)",
			cfg.LLM.Provider, configPath,
		)
	}
	return BuildRegressionGate3(profileStore, client, testCases, opts...)
}

// toLLMConfig converts an ares_config LLMConfig into an llm.Config.
func toLLMConfig(cfg *ares_config.LLMConfig) *llm.Config {
	return &llm.Config{
		Provider:        cfg.Provider,
		APIKey:          cfg.APIKey,
		BaseURL:         cfg.BaseURL,
		Model:           cfg.Model,
		Timeout:         cfg.Timeout,
		MaxTokens:       cfg.MaxTokens,
		MaxPromptLength: cfg.MaxPromptLength,
	}
}

// toLLMConfigs converts a primary LLMConfig plus its fallbacks into the ordered
// config list expected by llm.NewFailoverClient (primary first).
func toLLMConfigs(primary ares_config.LLMConfig) []*llm.Config {
	configs := make([]*llm.Config, 0, 1+len(primary.Fallbacks))
	configs = append(configs, toLLMConfig(&primary))
	for i := range primary.Fallbacks {
		configs = append(configs, toLLMConfig(&primary.Fallbacks[i]))
	}
	return configs
}
