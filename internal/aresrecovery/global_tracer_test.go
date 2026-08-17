package aresrecovery

import (
	"testing"
	"time"
)

// TestGlobalTracerTaskLifecycle verifies a task span records its lifecycle
// events and closes with a terminal status.
func TestGlobalTracerTaskLifecycle(t *testing.T) {
	tr := NewGlobalTracer()
	tr.TraceTask("t1", "created", nil)
	tr.TraceTask("t1", "acquired", map[string]any{"agent": "a1"})
	tr.TraceTask("t1", "run", nil)
	tr.Close("t1", "completed")

	span := tr.Span("t1")
	if span == nil {
		t.Fatal("span must exist")
	}
	if span.Kind != SpanTask || span.ID != "t1" {
		t.Fatalf("span kind/id wrong: %+v", span)
	}
	if span.Status != "completed" || span.EndedAt.IsZero() {
		t.Fatalf("span must be closed: %+v", span)
	}
	if len(span.Events) != 3 {
		t.Fatalf("want 3 events, got %d", len(span.Events))
	}
	if span.Events[1].Name != "acquired" {
		t.Fatalf("event 1 = %q, want acquired", span.Events[1].Name)
	}
	if tr.Count() != 1 {
		t.Fatalf("count = %d, want 1", tr.Count())
	}
}

// TestGlobalTracerMessageParentLink verifies a message span is linked to its
// parent task and records route events.
func TestGlobalTracerMessageParentLink(t *testing.T) {
	tr := NewGlobalTracer()
	tr.TraceMessage("corr-1", "sent", "t1", map[string]any{"to": "a2"})
	tr.TraceMessage("corr-1", "replied", "t1", nil)

	span := tr.Span("corr-1")
	if span == nil {
		t.Fatal("message span must exist")
	}
	if span.Kind != SpanMessage || span.ParentID != "t1" {
		t.Fatalf("message span wrong: %+v", span)
	}
	if len(span.Events) != 2 {
		t.Fatalf("want 2 route events, got %d", len(span.Events))
	}
}

// TestGlobalTracerByKind verifies spans are grouped by kind.
func TestGlobalTracerByKind(t *testing.T) {
	tr := NewGlobalTracer()
	tr.TraceTask("t1", "created", nil)
	tr.TraceAgent("a1", "spawned", nil)
	tr.TraceMessage("corr-1", "sent", "t1", nil)

	tasks := tr.ByKind(SpanTask)
	if len(tasks) != 1 || tasks[0].ID != "t1" {
		t.Fatalf("task spans wrong: %+v", tasks)
	}
	if len(tr.ByKind(SpanAgent)) != 1 {
		t.Fatalf("agent spans wrong")
	}
	if len(tr.ByKind(SpanMessage)) != 1 {
		t.Fatalf("message spans wrong")
	}
}

// TestGlobalTracerCloseUnknown verifies Close on an unknown span is a no-op.
func TestGlobalTracerCloseUnknown(t *testing.T) {
	tr := NewGlobalTracer()
	if got := tr.Close("missing", "completed"); got != nil {
		t.Fatalf("Close on unknown span must return nil, got %+v", got)
	}
	if tr.Span("missing") != nil {
		t.Fatal("unknown span must not exist")
	}
}

// TestGlobalTracerMaxSpans verifies span history is capped to the most recent
// n spans (oldest evicted).
func TestGlobalTracerMaxSpans(t *testing.T) {
	tr := NewGlobalTracer().WithMaxSpans(2)
	tr.TraceTask("t1", "created", nil)
	tr.TraceTask("t2", "created", nil)
	tr.TraceTask("t3", "created", nil) // t1 evicted

	if tr.Count() != 2 {
		t.Fatalf("count = %d, want 2", tr.Count())
	}
	if tr.Span("t1") != nil {
		t.Fatal("oldest span must be evicted")
	}
	if tr.Span("t2") == nil || tr.Span("t3") == nil {
		t.Fatal("recent spans must survive")
	}
}

// TestGlobalTracerConcurrentRecordingIsSafe verifies concurrent tracing from
// multiple goroutines is race-free and loses no events (-race). Task and
// agent spans use distinct ids so they never collide in the span map.
func TestGlobalTracerConcurrentRecordingIsSafe(t *testing.T) {
	tr := NewGlobalTracer()
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			taskID := "task-" + string(rune('a'+n))
			agentID := "agent-" + string(rune('a'+n))
			for i := 0; i < 50; i++ {
				tr.TraceTask(taskID, "step", nil)
				tr.TraceAgent(agentID, "step", nil)
			}
			tr.Close(taskID, "completed")
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}
	if tr.Count() != 16 {
		t.Fatalf("count = %d, want 16 spans", tr.Count())
	}
}

// TestGlobalTracerClock verifies the injectable clock drives event timestamps.
func TestGlobalTracerClock(t *testing.T) {
	fixed := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tr := NewGlobalTracer().WithClock(func() time.Time { return fixed })
	tr.TraceTask("t1", "created", nil)
	span := tr.Span("t1")
	if !span.Events[0].At.Equal(fixed) {
		t.Fatalf("event timestamp = %v, want %v", span.Events[0].At, fixed)
	}
}
