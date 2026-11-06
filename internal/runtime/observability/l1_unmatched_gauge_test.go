package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// TestSetL1UnmatchedOverrides_IsExported: the gauge exists so that "a declaration matches no tool"
// can be alerted on. A value written to an unregistered collector would never reach /metrics, which
// is the failure mode this asserts against.
func TestSetL1UnmatchedOverrides_IsExported(t *testing.T) {
	SetL1UnmatchedOverrides(2)
	if got := gatherGauge(t, "ARES_l1_unmatched_overrides"); got != 2 {
		t.Fatalf("gauge = %v, want 2", got)
	}

	// Zero must be published too: an absent series cannot be told apart from "healthy" by a
	// dashboard that only alerts on > 0, and stale non-zero values are worse than either.
	SetL1UnmatchedOverrides(0)
	if got := gatherGauge(t, "ARES_l1_unmatched_overrides"); got != 0 {
		t.Fatalf("gauge = %v, want 0", got)
	}
}

// gatherGauge reads one gauge out of the default registry, which is what /metrics serves.
func gatherGauge(t *testing.T, name string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, m := range f.GetMetric() {
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
		}
	}
	t.Fatalf("metric %q is not exported", name)
	return 0
}
