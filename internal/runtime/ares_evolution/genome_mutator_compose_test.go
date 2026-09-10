// genome_mutator_compose_test.go locks REVIEW 3.4#5: enabling the adaptive
// distribution used to overwrite the experience-guided mutator with one
// wrapping the RAW mutator, silently discarding experience-guided mutation
// whenever both were configured. The adaptive layer now drives the guided
// mutator through the AdaptiveMutater seam, so both features compose.
package evolution

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

func TestBuildMutatorComposesAdaptiveWithGuidance(t *testing.T) {
	var hintCalls atomic.Int32
	provider := &FuncGuidanceProvider{
		HintsFunc: func(ctx context.Context, taskType string, limit int) ([]EvolutionHint, error) {
			hintCalls.Add(1)
			return []EvolutionHint{
				{
					ID:         "hint-1",
					TaskType:   taskType,
					Confidence: 0.9,
					ParamHints: map[string]float64{"temperature": 0.3},
				},
			}, nil
		},
	}

	cfg := DefaultSystemConfig()
	cfg.EnableExperienceGuidedMutation = true
	cfg.GuidanceProvider = provider
	cfg.AdaptiveDistConfig = mutation.DefaultAdaptiveDistributionConfig()
	cfg.AdaptiveDistConfig.Enabled = true

	mutResult, err := buildMutator(cfg)
	require.NoError(t, err)
	require.NotNil(t, mutResult.adaptiveDist, "adaptive distribution must be built")
	require.NotNil(t, mutResult.genomeMut)

	// The composed mutator must consult the guidance provider: pre-fix the
	// adaptive distribution wrapped the RAW mutator and the provider was
	// never called.
	parent := &mutation.Strategy{ID: "parent", Version: 1}
	_, err = mutResult.genomeMut.Mutate(context.Background(), parent, 4)
	require.NoError(t, err)

	assert.Greater(t, hintCalls.Load(), int32(0),
		"the experience-guided mutator must participate when adaptive distribution is enabled")

	// Adaptive tuning must stay live: the distribution reports its current
	// probabilities and accepts outcome feedback.
	p1, p2, p3 := mutResult.adaptiveDist.CurrentProbabilities()
	assert.InDelta(t, 1.0, p1+p2+p3, 0.0001, "adaptive probabilities must stay normalized")
	assert.NotPanics(t, func() {
		mutResult.adaptiveDist.RecordOutcome(mutation.MutationParameter, 0.1, 0, true)
	})
}

func TestBuildMutatorAdaptiveOnlyWithoutGuidance(t *testing.T) {
	cfg := DefaultSystemConfig()
	cfg.EnableExperienceGuidedMutation = false
	cfg.AdaptiveDistConfig = mutation.DefaultAdaptiveDistributionConfig()
	cfg.AdaptiveDistConfig.Enabled = true

	mutResult, err := buildMutator(cfg)
	require.NoError(t, err)
	require.NotNil(t, mutResult.adaptiveDist)

	// Without guidance the adaptive layer wraps the raw mutator; mutation
	// still works.
	parent := &mutation.Strategy{ID: "parent", Version: 1}
	children, err := mutResult.genomeMut.Mutate(context.Background(), parent, 3)
	require.NoError(t, err)
	assert.Len(t, children, 3)
}
