package sub

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
)

// newTestExecutor builds a taskExecutor whose template engine mirrors the
// production wiring (NewTemplateEngine) so renderPrompt is exercised against
// the same renderer the server uses.
func newTestExecutor(promptTpl string) *taskExecutor {
	return &taskExecutor{
		template:  output.NewTemplateEngine(),
		promptTpl: promptTpl,
	}
}

// TestRenderPrompt_DefaultTemplateCarriesTaskInput is the regression guard for
// the empty-prompt 400: the config default template references {{.input}}, and
// renderPrompt must supply it from the planner's task_desc payload. Before the
// fix, a config without prompts.recommendation rendered an empty prompt and
// every worker LLM call failed with a provider 400 (empty user content),
// burning the failover cooldown (20s) per call.
func TestRenderPrompt_DefaultTemplateCarriesTaskInput(t *testing.T) {
	e := newTestExecutor(ares_config.DefaultRecommendationPrompt)
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})
	task.Payload = map[string]any{"task_desc": "Plan a trip to Kyoto"}

	prompt, err := e.renderPrompt(e.promptTpl, task, task.UserProfile)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "Plan a trip to Kyoto")
	assert.Contains(t, prompt, "travel")
}

// TestRenderPrompt_DefaultTemplateFallbackToAgentType verifies that a task
// without a task_desc payload still renders a non-empty prompt (the agent
// type is used as the input stand-in), so the fail-fast guard is not tripped
// by legacy tasks.
func TestRenderPrompt_DefaultTemplateFallbackToAgentType(t *testing.T) {
	e := newTestExecutor(ares_config.DefaultRecommendationPrompt)
	task := models.NewTask("t1", models.AgentType("coder"), &models.UserProfile{})

	prompt, err := e.renderPrompt(e.promptTpl, task, task.UserProfile)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)
	assert.Contains(t, prompt, "coder")
}

// TestRenderPrompt_EmptyTemplateFailsFast locks the fail-fast contract: an
// empty template (config omission) must surface as an explicit wiring error
// instead of silently sending an empty user message to the provider.
func TestRenderPrompt_EmptyTemplateFailsFast(t *testing.T) {
	e := newTestExecutor("")
	task := models.NewTask("t1", models.AgentType("travel"), &models.UserProfile{})

	_, err := e.renderPrompt("", task, task.UserProfile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty prompt")
}

// TestRenderPrompt_ProfilePreferencesInjected verifies that profile
// preferences flow into the template data under their lowercase keys.
func TestRenderPrompt_ProfilePreferencesInjected(t *testing.T) {
	e := newTestExecutor("Preferences: {{.preferences}} budget: {{.budget}}")
	profile := &models.UserProfile{
		Preferences: map[string]any{"preferences": "minimalist"},
	}
	task := models.NewTask("t1", models.AgentType("shopping"), profile)

	prompt, err := e.renderPrompt(e.promptTpl, task, profile)
	require.NoError(t, err)
	assert.Contains(t, prompt, "minimalist")
	assert.NotEmpty(t, strings.TrimSpace(prompt))
}

// TestRenderPrompt_HandlesNilProfile verifies the executor never panics on a
// nil profile during rendering (defense in depth for degraded wiring).
func TestRenderPrompt_HandlesNilProfile(t *testing.T) {
	e := newTestExecutor(ares_config.DefaultRecommendationPrompt)
	task := models.NewTask("t1", models.AgentType("travel"), nil)

	prompt, err := e.renderPrompt(e.promptTpl, task, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, prompt)
}
