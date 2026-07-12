package leader

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordAdapter is a fake output.LLMAdapter that captures the rendered
// prompt and the per-call params forwarded to GenerateWithParams.
type recordAdapter struct {
	prompt   string
	params   map[string]any
	response string
	err      error
}

func (r *recordAdapter) Generate(ctx context.Context, prompt string) (string, error) {
	return r.response, r.err
}
func (r *recordAdapter) GenerateWithParams(ctx context.Context, prompt string, params map[string]any) (string, error) {
	r.prompt = prompt
	r.params = params
	return r.response, r.err
}
func (r *recordAdapter) GenerateStructured(ctx context.Context, prompt string, schema string) (*models.RecommendResult, error) {
	return nil, nil
}
func (r *recordAdapter) GenerateStream(ctx context.Context, prompt string) (<-chan output.StreamChunk, error) {
	return nil, nil
}
func (r *recordAdapter) GetModel() string { return "test-model" }

var _ output.LLMAdapter = (*recordAdapter)(nil)

// fakeStrategySource is a test double for agents.StrategySource.
type fakeStrategySource struct {
	st *agents.ActiveStrategy
}

func (f fakeStrategySource) GetActiveStrategy(ctx context.Context) (*agents.ActiveStrategy, error) {
	return f.st, nil
}

func TestProfileParser_ActiveStrategyOverride(t *testing.T) {
	rec := &recordAdapter{response: `{"style":["casual"],"budget":5000}`}
	p := &profileParser{
		llmAdapter: rec,
		template:   output.NewTemplateEngine(),
		promptTpl:  "default: {{.input}}",
		validator:  nil,
		maxRetries: 1,
	}
	p.WithStrategySource(fakeStrategySource{st: &agents.ActiveStrategy{
		ID:     "gen-3",
		Prompt: "OVERRIDE: {{.input}}",
		Params: map[string]any{"temperature": 0.2},
	}})

	_, err := p.parseOnce(context.Background(), "I like hiking")
	require.NoError(t, err)

	// The overridden template must have been rendered into the prompt.
	assert.Contains(t, rec.prompt, "OVERRIDE")
	// Per-call params must have been forwarded to the LLM call.
	require.NotNil(t, rec.params)
	assert.Equal(t, 0.2, rec.params["temperature"])
}

func TestProfileParser_NoStrategyUsesDefaults(t *testing.T) {
	rec := &recordAdapter{response: `{"budget":5000}`}
	p := &profileParser{
		llmAdapter: rec,
		template:   output.NewTemplateEngine(),
		promptTpl:  "default: {{.input}}",
		validator:  nil,
		maxRetries: 1,
	}

	_, err := p.parseOnce(context.Background(), "hi")
	require.NoError(t, err)

	assert.Contains(t, rec.prompt, "default:")
	// No strategy -> no override params forwarded.
	assert.Empty(t, rec.params)
}
