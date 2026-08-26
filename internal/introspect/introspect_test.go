package introspect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/kernelscheduler"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// TestCollectorAssemblesDomains verifies that Collect maps all three source
// domains into one frame and increments the sequence.
func TestCollectorAssemblesDomains(t *testing.T) {
	c := NewCollector(Sources{
		Kernel: func() kernelscheduler.SchedulerSnapshot {
			return kernelscheduler.SchedulerSnapshot{Scheduled: 7, ReadyTasks: 2}
		},
		Fabric: func() []taskfabric.LeaseEntry {
			return []taskfabric.LeaseEntry{{TaskID: "t-1", State: taskfabric.StateReady}}
		},
		Agents: func() []agentfabric.AgentView {
			return []agentfabric.AgentView{{Identity: "a", State: agentfabric.StateIdle}}
		},
	})

	snap := c.Collect()
	if snap.Seq != 1 {
		t.Fatalf("seq = %d, want 1", snap.Seq)
	}
	if snap.Kernel.Scheduled != 7 || snap.Kernel.ReadyTasks != 2 {
		t.Errorf("kernel domain not mapped: %+v", snap.Kernel)
	}
	if len(snap.Fabric) != 1 || snap.Fabric[0].TaskID != "t-1" {
		t.Errorf("fabric domain not mapped: %+v", snap.Fabric)
	}
	if len(snap.Agents) != 1 || snap.Agents[0].Identity != "a" {
		t.Errorf("agents domain not mapped: %+v", snap.Agents)
	}
}

// TestStoreHoldsLatestOnly locks the O(1) memory contract: Set overwrites and
// Latest always returns the newest frame.
func TestStoreHoldsLatestOnly(t *testing.T) {
	var s Store
	if s.Latest() != nil {
		t.Fatal("Latest before first Set must be nil")
	}
	s.Set(Snapshot{Seq: 1})
	s.Set(Snapshot{Seq: 2})
	got := s.Latest()
	if got == nil || got.Seq != 2 {
		t.Fatalf("latest seq = %+v, want 2", got)
	}
}

// TestHandlerRoutes covers the three route behaviors: UI page, JSON snapshot,
// and 503 before the collector's first tick.
func TestHandlerRoutes(t *testing.T) {
	var store Store
	h := NewHandler(&store)

	t.Run("snapshot_503_before_first_collect", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("got %d, want 503", w.Code)
		}
	})

	store.Set(Snapshot{Seq: 9, TS: time.Now()})

	t.Run("snapshot_json", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", w.Code)
		}
		var snap Snapshot
		if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if snap.Seq != 9 {
			t.Errorf("seq = %d, want 9", snap.Seq)
		}
	})

	t.Run("ui_page_served", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/introspect", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("got %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Errorf("content-type = %q", ct)
		}
		if len(w.Body.Bytes()) < 500 {
			t.Error("panel html suspiciously small")
		}
	})

	t.Run("unknown_path_404", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/introspect/nope", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("got %d, want 404", w.Code)
		}
	})
}

// TestConcurrentCollectSetLatest exercises the race surface the serve loop
// will hit: collector writing while HTTP readers poll (go test -race judge).
func TestConcurrentCollectSetLatest(t *testing.T) {
	c := NewCollector(Sources{Fabric: func() []taskfabric.LeaseEntry { return nil }})
	var store Store
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			store.Set(c.Collect())
		}
	}()
	for i := 0; i < 200; i++ {
		_ = store.Latest()
	}
	<-done
	if c.seq.Load() != 200 {
		t.Fatalf("seq = %d, want 200", c.seq.Load())
	}
}
