// Package soak runs long-duration stability checks (Phase 1 of
// plan/stability_performance_plan.md). Skipped unless SOAK_SECONDS is set:
// `make check` must not pay multi-minute runs. Real soaks:
//
//	SOAK_SECONDS=3600 go test -count=1 -run TestSoakSteadyState ./tests/soak/
//
// The workload churns the goroutine-spawning paths the leak program hardened
// (event store subscriptions, the workflow reloader's fsnotify loop, the
// load tracker, fabric task lifecycle) and samples goroutine count, file
// descriptors and GC'd heap once per second. A stable process shows a flat
// last third; a leak shows monotonic growth the assertions reject.
package soak

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/kernel"
)

// sample is one stability observation.
type sample struct {
	goroutines int
	fds        int
	heapBytes  uint64
}

// fdCount counts open file descriptors for the current process.
func fdCount(t *testing.T) int {
	t.Helper()
	for _, dir := range []string{"/dev/fd", "/proc/self/fd"} {
		entries, err := os.ReadDir(dir)
		if err == nil {
			return len(entries)
		}
	}
	t.Log("fd directory not readable; fd stability check degraded to goroutines+heap")
	return -1
}

// observe takes one sample: goroutines, fds, and the heap after a forced GC
// (so allocator churn cannot masquerade as a leak).
func observe(t *testing.T) sample {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return sample{
		goroutines: runtime.NumGoroutine(),
		fds:        fdCount(t),
		heapBytes:  ms.HeapAlloc,
	}
}

// workflowFile is a minimal loadable workflow for the reloader churn.
func workflowFile(name string) []byte {
	def := map[string]any{
		"id":   name,
		"name": name,
		"steps": []map[string]any{
			{"id": "s1", "type": "task", "task": "noop"},
		},
	}
	data, _ := json.Marshal(def)
	return data
}

// TestSoakSteadyStateUnderMixedLoad asserts the process reaches a steady
// state under sustained churn: the last third of the run must not exceed the
// first third by more than a small fixed tolerance on any resource axis.
func TestSoakSteadyStateUnderMixedLoad(t *testing.T) {
	seconds := 0
	if v := os.Getenv("SOAK_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			t.Fatalf("SOAK_SECONDS must be a non-negative integer, got %q", v)
		}
		seconds = n
	}
	if seconds < 10 {
		t.Skipf("soak disabled: set SOAK_SECONDS=<N> (>=10, e.g. 3600) to run, got %q", os.Getenv("SOAK_SECONDS"))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --- workload 1: event store subscribe/append churn ---
	store := ares_events.NewMemoryEventStore()
	go func() {
		stream := 0
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
			stream++
			ev := &ares_events.Event{
				ID:       fmt.Sprintf("ev-%d", i),
				StreamID: fmt.Sprintf("soak-stream-%d", stream%64),
				Type:     ares_events.EventType("soak.tick"),
				Payload:  map[string]any{"i": i},
			}
			_ = store.Append(ctx, ev.StreamID, []*ares_events.Event{ev}, int64(i))
			if i%50 == 0 {
				subCtx, subCancel := context.WithCancel(ctx)
				ch, err := store.Subscribe(subCtx, ares_events.EventFilter{})
				if err == nil {
					go func() {
						for range ch {
						}
					}()
				}
				subCancel()
			}
		}
	}()

	// --- workload 2: reloader fsnotify churn ---
	watchDir := t.TempDir()
	watcher, err := engine.NewFileWatcher(engine.NewJSONFileLoader(), map[string]*engine.Workflow{})
	if err != nil {
		t.Fatalf("NewFileWatcher: %v", err)
	}
	t.Cleanup(watcher.Close)
	watchCtx, watchCancel := context.WithCancel(ctx)
	defer watchCancel()
	if err := watcher.Watch(watchCtx, watchDir); err != nil {
		t.Fatalf("Watch: %v", err)
	}
	go func() {
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			// Rewrite one of a few workflow files: fsnotify events →
			// scanAndLoad → callbacks, continuously.
			name := filepath.Join(watchDir, fmt.Sprintf("wf-%d.json", i%4))
			_ = os.WriteFile(name, workflowFile(fmt.Sprintf("wf-%d", i%4)), 0o600)
		}
	}()

	// --- workload 3: load tracker churn with rotating agents ---
	tracker := kernel.NewLoadTracker()
	go func() {
		gen := 0
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
			agent := fmt.Sprintf("soak-agent-%d-%d", gen, i%8)
			tracker.Begin(agent)
			tracker.End(agent, i%3 != 0)
			if i%200 == 0 {
				// Rotate the generation: every tracked agent is forgotten,
				// exercising the straggler-guard path (N-4).
				gen++
				for j := 0; j < 8; j++ {
					tracker.Forget(fmt.Sprintf("soak-agent-%d-%d", gen-1, j))
				}
				_ = tracker.Snapshot()
			}
		}
	}()

	// --- workload 4: fabric task lifecycle churn ---
	fabric := taskfabric.NewFabric()
	go func() {
		for i := 0; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(15 * time.Millisecond):
			}
			id := fmt.Sprintf("soak-task-%d", i)
			if err := fabric.Create(&taskfabric.Task{ID: id, Capability: "soak"}); err == nil {
				_ = fabric.Delete(id)
			}
		}
	}()

	// --- sampling + assertions ---
	// Warm-up: let every loop start and allocate its steady-state set, then
	// take the first-third baseline. Tolerances absorb jitter (a stray
	// subscription mid-sample, one fsnotify event in flight) without hiding
	// a real trend: a leak grows monotonically and blows through them.
	time.Sleep(2 * time.Second)
	samples := make([]sample, 0, seconds)
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for time.Now().Before(deadline) {
		samples = append(samples, observe(t))
		time.Sleep(1 * time.Second)
	}
	cancel()

	third := len(samples) / 3
	if third < 2 {
		t.Fatalf("SOAK_SECONDS=%d yields only %d samples; need >= 10s", seconds, len(samples))
	}
	first, last := samples[:third], samples[len(samples)-third:]

	maxG := 0
	for _, s := range first {
		if s.goroutines > maxG {
			maxG = s.goroutines
		}
	}
	for i, s := range last {
		if s.goroutines > maxG+3 {
			t.Fatalf("goroutine leak: last-third sample %d has %d goroutines, first-third max %d (+3 tolerance)",
				i, s.goroutines, maxG)
		}
	}

	if first[0].fds >= 0 {
		maxF := 0
		for _, s := range first {
			if s.fds > maxF {
				maxF = s.fds
			}
		}
		for i, s := range last {
			if s.fds > maxF+3 {
				t.Fatalf("fd leak: last-third sample %d has %d fds, first-third max %d (+3 tolerance)",
					i, s.fds, maxF)
			}
		}
	}

	var firstHeap uint64
	for _, s := range first {
		if s.heapBytes > firstHeap {
			firstHeap = s.heapBytes
		}
	}
	for i, s := range last {
		if s.heapBytes > firstHeap*2+16<<20 {
			t.Fatalf("heap leak: last-third sample %d has %d heap bytes after GC, first-third max %d (2x + 16MiB tolerance)",
				i, s.heapBytes, firstHeap)
		}
	}

	t.Logf("soak steady: %d samples, goroutines<=%d(+3), fds<=%d(+3), heap<=%d(2x+16MiB)",
		len(samples), maxG, first[0].fds, firstHeap)
}
