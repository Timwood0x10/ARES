// nolint: errcheck // Test code may ignore return values
package ares_observability

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatherFloat reads a single gauge value by metric name + label set from an
// isolated registry, avoiding the prometheus/testutil dependency. It returns 0
// when the metric/label is absent.
func gatherFloat(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	fams, err := reg.Gather()
	require.NoError(t, err)
	for _, fam := range fams {
		if fam.GetName() != name {
			continue
		}
		for _, m := range fam.GetMetric() {
			match := true
			for _, lp := range m.GetLabel() {
				if want, ok := labels[lp.GetName()]; ok && lp.GetValue() != want {
					match = false
					break
				}
			}
			if match {
				return m.GetGauge().GetValue()
			}
		}
	}
	return 0
}

// TestSetAKGSnapshot verifies the bridge from the compiler's quality-gate
// collector to Prometheus: a Snapshot is pushed onto the AKG gauges via an
// isolated registry, and a repeated push overwrites (Set semantics) rather than
// accumulating across runs.
func TestSetAKGSnapshot(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := &PrometheusMetrics{
		AKGObjectsIn:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "ARES_akg_objects_in"}),
		AKGDroppedStructural: prometheus.NewGauge(prometheus.GaugeOpts{Name: "ARES_akg_dropped_structural"}),
		AKGDroppedLowConf:    prometheus.NewGauge(prometheus.GaugeOpts{Name: "ARES_akg_dropped_lowconf"}),
		AKGDedupHits:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "ARES_akg_dedup_hits"}),
		AKGObjectsBuilt:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "ARES_akg_objects_built"}),
		AKGConfidenceBucket:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ARES_akg_confidence_bucket"}, []string{"bucket"}),
		AKGSignalTier:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "ARES_akg_signal_tier"}, []string{"tier"}),
	}
	reg.MustRegister(
		m.AKGObjectsIn, m.AKGDroppedStructural, m.AKGDroppedLowConf,
		m.AKGDedupHits, m.AKGObjectsBuilt, m.AKGConfidenceBucket, m.AKGSignalTier,
	)

	snap := &compiler.AKGSnapshot{
		NodesIn:             6,
		DroppedLowConf:      2,
		DroppedStructural:   2,
		DedupHits:           1,
		ObjectsBuilt:        2,
		ConfidenceHistogram: map[string]int64{"0.9-1.0": 2},
		SignalTiers:         map[string]int64{"strong": 2},
	}
	m.SetAKGSnapshot(snap)

	assert.Equal(t, float64(6), gatherFloat(t, reg, "ARES_akg_objects_in", nil))
	assert.Equal(t, float64(2), gatherFloat(t, reg, "ARES_akg_dropped_lowconf", nil))
	assert.Equal(t, float64(2), gatherFloat(t, reg, "ARES_akg_dropped_structural", nil))
	assert.Equal(t, float64(1), gatherFloat(t, reg, "ARES_akg_dedup_hits", nil))
	assert.Equal(t, float64(2), gatherFloat(t, reg, "ARES_akg_objects_built", nil))
	assert.Equal(t, float64(2), gatherFloat(t, reg, "ARES_akg_confidence_bucket", map[string]string{"bucket": "0.9-1.0"}))
	assert.Equal(t, float64(2), gatherFloat(t, reg, "ARES_akg_signal_tier", map[string]string{"tier": "strong"}))

	// A second push with a smaller snapshot must OVERWRITE, not accumulate:
	// the gate's gauges reflect the latest run, not a running total.
	m.SetAKGSnapshot(&compiler.AKGSnapshot{NodesIn: 1})
	assert.Equal(t, float64(1), gatherFloat(t, reg, "ARES_akg_objects_in", nil))
	assert.Equal(t, float64(0), gatherFloat(t, reg, "ARES_akg_dropped_lowconf", nil), "stale value cleared by Set")
	assert.Equal(t, float64(0), gatherFloat(t, reg, "ARES_akg_objects_built", nil))
}
