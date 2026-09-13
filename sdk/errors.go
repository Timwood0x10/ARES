package sdk

import (
	"errors"
	"fmt"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// ErrHumanInputUnsupported is returned by Agent.Run and Agent.Stream when the
// agent was built with WithHumanInput.
//
// The approval hook has no interception point on the shared L2 execution path,
// so it cannot be honoured. Refusing the run is the only honest option: an
// ignored hook leaves the caller believing a destructive-tool gate is in force
// (see docs/reviews/0.3.1-final-deep-review.md §5.2 E-2).
var ErrHumanInputUnsupported = errors.New("sdk: WithHumanInput is not supported on the L2 execution path")

// FriendlyErr wraps an LLM error with an actionable hint based on the
// provider. Localized from the retired internal/agentloop engine (B4): the
// sdk is now the single owner of the hint table, used by New() and by every
// LLM-touching path.
func FriendlyErr(scope string, provider llmcore.LLMProvider, origErr error) error {
	hints := map[llmcore.LLMProvider]string{
		llmcore.LLMProviderOpenAI:     "→ Set OPENAI_API_KEY or check https://platform.openai.com/account/api-keys",
		llmcore.LLMProviderAnthropic:  "→ Set ANTHROPIC_API_KEY or check https://console.anthropic.com/",
		llmcore.LLMProviderOpenRouter: "→ Set OPENROUTER_API_KEY or check https://openrouter.ai/keys",
		llmcore.LLMProviderOllama:     "→ Run: ollama run llama3.2  (Ollama may not be running)",
	}
	// The underlying error rides ONCE, via %w (errors.Is/As must be able to
	// match the cause).
	if hint, ok := hints[provider]; ok {
		return fmt.Errorf("%s: %w\n  %s", scope, origErr, hint)
	}
	return fmt.Errorf("%s: %w", scope, origErr)
}
