package evolution

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestWiredEvolutionSystem_ArmsTheExperimentLogWhenConfigured covers the assembly hop: a
// configured path must actually open a log and hand it to the adapter. Without this, the
// producer and the path-forwarding can both be correct while nothing is ever sealed.
func TestWiredEvolutionSystem_ArmsTheExperimentLogWhenConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.jsonl")

	cfg := DefaultSystemConfig()
	cfg.ExperimentLogPath = path

	system, err := NewWiredEvolutionSystem(
		&mutation.Strategy{ID: "base", Params: map[string]any{"temperature": 0.7}, Score: 0.5}, cfg)
	require.NoError(t, err)
	require.NotNil(t, system.PopAdapter)
	require.NotNil(t, system.PopAdapter.experimentLog, "a configured path must arm the log")

	// Opening with O_CREATE is what creates the file; a missing file here would mean the log
	// was constructed but never opened.
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "opening the experiment log creates the file")
}

// TestWiredEvolutionSystem_NoPathMeansNoLog pins the default: unset path, no sealing, and the
// adapter behaves exactly as it did before this feature existed.
func TestWiredEvolutionSystem_NoPathMeansNoLog(t *testing.T) {
	cfg := DefaultSystemConfig()

	system, err := NewWiredEvolutionSystem(&mutation.Strategy{ID: "base"}, cfg)
	require.NoError(t, err)
	require.NotNil(t, system.PopAdapter)
	assert.Nil(t, system.PopAdapter.experimentLog, "no path means no sealing")
}
