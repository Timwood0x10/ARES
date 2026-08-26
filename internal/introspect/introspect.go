// Package introspect implements the runtime introspection panel read-model
// (monitoring.md): it periodically PULLS point-in-time snapshots from the
// kernel scheduler, task fabric and agent fabric, keeps only the LATEST one
// (bounded memory), and serves both a JSON API (/api/v1/introspect/*) and an
// embedded single-page UI. It is strictly read-only — sources expose snapshot
// methods, never write paths.
package introspect

import (
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/kernelscheduler"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// Snapshot is one full panel refresh: everything the UI renders in a frame.
type Snapshot struct {
	// TS is when the collector produced this snapshot.
	TS time.Time `json:"ts"`
	// Seq is a monotonically increasing frame counter.
	Seq uint64 `json:"seq"`
	// Kernel is the scheduler's Domain A view.
	Kernel kernelscheduler.SchedulerSnapshot `json:"kernel"`
	// Fabric is the task/lease Domain B view.
	Fabric []taskfabric.LeaseEntry `json:"fabric"`
	// Agents is the lifecycle Domain C view.
	Agents []agentfabric.AgentView `json:"agents"`
}

// Sources abstract the three subsystems so tests can fake them (code_rules_v2
// §5.2: interfaces defined at the consumer).
type Sources struct {
	Kernel func() kernelscheduler.SchedulerSnapshot
	Fabric func() []taskfabric.LeaseEntry
	Agents func() []agentfabric.AgentView
}

// Collector produces Snapshots from Sources.
type Collector struct {
	src Sources
	seq atomic.Uint64
}

// NewCollector builds a collector over the given sources.
func NewCollector(src Sources) *Collector {
	return &Collector{src: src}
}

// Collect assembles one Snapshot. A nil source function yields its zero value
// so a partially wired runtime still renders (missing domains show empty).
func (c *Collector) Collect() Snapshot {
	snap := Snapshot{TS: time.Now(), Seq: c.seq.Add(1)}
	if c.src.Kernel != nil {
		snap.Kernel = c.src.Kernel()
	}
	if c.src.Fabric != nil {
		snap.Fabric = c.src.Fabric()
	}
	if c.src.Agents != nil {
		snap.Agents = c.src.Agents()
	}
	return snap
}

// Store holds the latest snapshot. Memory stays O(1) by design (monitoring.md
// §3.1 principle 3: bounded read-model) — history lives in the event log, not
// here.
type Store struct {
	latest atomic.Pointer[Snapshot]
}

// Set publishes a new latest snapshot.
func (s *Store) Set(snap Snapshot) { s.latest.Store(&snap) }

// Latest returns the most recent snapshot, or nil before the first collect.
func (s *Store) Latest() *Snapshot { return s.latest.Load() }
