package main

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"

	"github.com/Timwood0x10/ares/internal/ares_config"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestUnmatchedToolClassOverrides: a typo in kernel.l1.tools is otherwise invisible — the entry
// validates, is kept, and never applies because no tool by that name exists. The startup warning is
// the only signal that the declaration was not honoured.
func TestUnmatchedToolClassOverrides(t *testing.T) {
	t.Cleanup(func() { setToolClassOverrides(nil) })

	attempts := 1
	setToolClassOverrides(map[string]ares_config.L1ToolOverride{
		"rg":     {MaxAttempts: &attempts},
		"rgx":    {MaxAttempts: &attempts}, // typo: nothing is registered under this name
		"gitlab": {},
	})

	schemas := []core_tools.ToolSchema{{Name: "rg"}, {Name: "git"}}

	assert.Equal(t, []string{"gitlab", "rgx"}, unmatchedToolClassOverrides(schemas),
		"names must be sorted so the log line is stable across runs")

	// Everything configured is registered: nothing to report.
	assert.Empty(t, unmatchedToolClassOverrides([]core_tools.ToolSchema{{Name: "rg"}, {Name: "rgx"}, {Name: "gitlab"}}))

	// No configuration at all: nothing to report either.
	setToolClassOverrides(nil)
	assert.Empty(t, unmatchedToolClassOverrides(schemas))
}

// TestReportUnmatchedToolClassOverrides_SetsTheGauge: the warning alone is not a signal anyone acts
// on at 3am. The report helper must also publish the count, and publish ZERO when everything
// matches — a stale non-zero value would page someone about a typo that was already fixed.
func TestReportUnmatchedToolClassOverrides_SetsTheGauge(t *testing.T) {
	t.Cleanup(func() { setToolClassOverrides(nil) })

	attempts := 1
	setToolClassOverrides(map[string]ares_config.L1ToolOverride{
		"rg":  {MaxAttempts: &attempts},
		"rgx": {MaxAttempts: &attempts},
	})

	reportUnmatchedToolClassOverrides(context.Background(), []core_tools.ToolSchema{{Name: "rg"}})
	if got := readL1Gauge(t); got != 1 {
		t.Fatalf("gauge = %v, want 1 (one configured name is not registered)", got)
	}

	setToolClassOverrides(map[string]ares_config.L1ToolOverride{"rg": {MaxAttempts: &attempts}})
	reportUnmatchedToolClassOverrides(context.Background(), []core_tools.ToolSchema{{Name: "rg"}})
	if got := readL1Gauge(t); got != 0 {
		t.Fatalf("gauge = %v, want 0 after the declaration matches", got)
	}
}

// readL1Gauge reads the unmatched-overrides gauge from the registry /metrics serves.
func readL1Gauge(t *testing.T) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "ARES_l1_unmatched_overrides" {
			continue
		}
		for _, m := range f.GetMetric() {
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
		}
	}
	t.Fatal("ARES_l1_unmatched_overrides is not exported")
	return 0
}
