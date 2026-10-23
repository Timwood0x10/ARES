package ares_bootstrap

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	aresconfig "github.com/Timwood0x10/ares/internal/ares_config"
)

// captureLogs redirects the default logger into a buffer for the duration of the test.
func captureObjectiveLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestLogEvolutionObjective_StrategySaysThePathIsOff pins the startup announcement: a
// strategy-only process must say so, because otherwise it is indistinguishable from one that
// applies runtime patches until a patch actually lands.
//
// The assertion is on module+method as well as the message: the repository convention is that the
// message stays a fixed phrase and the source is carried as structured fields, so a log line
// without them is a line nobody can grep for when debugging a deployment.
func TestLogEvolutionObjective_StrategySaysThePathIsOff(t *testing.T) {
	buf := captureObjectiveLogs(t)

	logEvolutionObjective(context.Background(), aresconfig.EvolutionConfig{}) // empty = strategy

	out := buf.String()
	assert.Contains(t, out, "runtime-patch path disabled")
	// The module is carried by the logger instance (logger.Module), not by an attribute, so the
	// rendered line names the subsystem once.
	assert.Contains(t, out, "module=ares_bootstrap.evolution")
	assert.Contains(t, out, "method=assembleGAConfig")
	assert.Contains(t, out, "objective=strategy")
	assert.NotContains(t, out, "WARN", "the narrow default is not a warning: it is the safe case")
}

// TestLogEvolutionObjective_RuntimeWarns: the wider objectives must announce that patches reach a
// running system — the one fact an operator needs before a deployment changes behaviour.
func TestLogEvolutionObjective_RuntimeWarns(t *testing.T) {
	buf := captureObjectiveLogs(t)

	logEvolutionObjective(context.Background(), aresconfig.EvolutionConfig{
		Objective:      aresconfig.EvolutionObjectiveBoth,
		RuntimePatches: aresconfig.RuntimePatchConfig{AllowLive: true},
	})

	out := buf.String()
	assert.True(t, strings.Contains(out, "WARN"), "applying to a live system is a warning")
	assert.Contains(t, out, "apply to the live system")
	assert.Contains(t, out, "module=ares_bootstrap.evolution")
	assert.Contains(t, out, "method=assembleGAConfig")
	assert.Contains(t, out, "allow_live=true")
}
