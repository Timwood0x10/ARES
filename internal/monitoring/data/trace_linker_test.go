package data

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecord_NilEvent(t *testing.T) {
	tl := NewTraceLinker()
	// Should not panic.
	tl.Record(nil)
	assert.Empty(t, tl.ListTraces())
}

func TestRecord_AgentStartStop(t *testing.T) {
	tests := []struct {
		name         string
		startPayload map[string]any
		stopPayload  map[string]any
		wantTraceID  string
		wantAgentID  string
	}{
		{
			name:         "with agent_id",
			startPayload: map[string]any{"agent_id": "a1"},
			stopPayload:  map[string]any{"agent_id": "a1"},
			wantTraceID:  "a1",
			wantAgentID:  "a1",
		},
		{
			name:         "fallback to stream ID",
			startPayload: map[string]any{},
			stopPayload:  map[string]any{},
			wantTraceID:  "stream-1",
			wantAgentID:  "stream-1",
		},
		{
			name:         "with trace_id in payload",
			startPayload: map[string]any{"agent_id": "a1", "trace_id": "trace-abc"},
			stopPayload:  map[string]any{"agent_id": "a1"},
			wantTraceID:  "trace-abc",
			wantAgentID:  "a1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl := NewTraceLinker()
			now := time.Now()

			tl.Record(&ares_events.Event{
				ID: "evt-1", StreamID: "stream-1",
				Type:    ares_events.EventAgentStarted,
				Payload: tt.startPayload, Timestamp: now,
			})

			spans := tl.GetTrace(tt.wantTraceID)
			require.Len(t, spans, 1)
			assert.Equal(t, "agent.start", spans[0].Name)
			assert.Equal(t, tt.wantAgentID, spans[0].AgentID)
			assert.Equal(t, "ok", spans[0].Status)
			assert.Equal(t, now, spans[0].StartTime)
			assert.True(t, spans[0].EndTime.IsZero())

			tl.Record(&ares_events.Event{
				ID: "evt-2", StreamID: "stream-1",
				Type:    ares_events.EventAgentStopped,
				Payload: tt.stopPayload, Timestamp: now.Add(5 * time.Second),
			})

			spans = tl.GetTrace(tt.wantTraceID)
			require.Len(t, spans, 1)
			assert.False(t, spans[0].EndTime.IsZero())
			assert.Equal(t, 5*time.Second, spans[0].Duration)
		})
	}
}

func TestRecord_AgentStopWithoutStart(t *testing.T) {
	tl := NewTraceLinker()
	// Closing a span that was never opened should be a no-op.
	tl.Record(&ares_events.Event{
		ID: "evt-1", StreamID: "s1",
		Type:    ares_events.EventAgentStopped,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: time.Now(),
	})
	assert.Empty(t, tl.ListTraces())
}

func TestRecord_ToolCallStartComplete(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "evt-1", StreamID: "s1",
		Type:      ares_events.EventToolCallStarted,
		Payload:   map[string]any{"agent_id": "a1", "tool_name": "read_file"},
		Timestamp: now,
	})

	spans := tl.GetTrace("a1")
	require.Len(t, spans, 1)
	assert.Equal(t, "tool.call.read_file", spans[0].Name)
	assert.True(t, spans[0].EndTime.IsZero())

	tl.Record(&ares_events.Event{
		ID: "evt-2", StreamID: "s1",
		Type:      ares_events.EventToolCallCompleted,
		Payload:   map[string]any{"agent_id": "a1", "tool_name": "read_file"},
		Timestamp: now.Add(2 * time.Second),
	})

	spans = tl.GetTrace("a1")
	require.Len(t, spans, 1)
	assert.False(t, spans[0].EndTime.IsZero())
	assert.Equal(t, 2*time.Second, spans[0].Duration)
}

func TestRecord_ToolCallMissingName(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "evt-1", StreamID: "s1",
		Type:    ares_events.EventToolCallStarted,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})

	spans := tl.GetTrace("a1")
	require.Len(t, spans, 1)
	assert.Equal(t, "tool.call.unknown", spans[0].Name)
}

func TestRecord_ToolCallCompleteWithoutStart(t *testing.T) {
	tl := NewTraceLinker()
	// Should be a no-op.
	tl.Record(&ares_events.Event{
		ID: "evt-1", StreamID: "s1",
		Type:    ares_events.EventToolCallCompleted,
		Payload: map[string]any{"agent_id": "a1", "tool_name": "write"}, Timestamp: time.Now(),
	})
	assert.Empty(t, tl.ListTraces())
}

func TestRecord_LLMCall(t *testing.T) {
	tests := []struct {
		name        string
		payload     map[string]any
		wantDur     time.Duration
		wantEndTime time.Time
	}{
		{
			name:        "with duration in payload",
			payload:     map[string]any{"agent_id": "a1", "duration": float64(3 * time.Second)},
			wantDur:     3 * time.Second,
			wantEndTime: time.Date(2025, 1, 1, 0, 0, 3, 0, time.UTC),
		},
		{
			name:        "without duration",
			payload:     map[string]any{"agent_id": "a1"},
			wantDur:     0,
			wantEndTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl := NewTraceLinker()
			base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

			tl.Record(&ares_events.Event{
				ID: "evt-1", StreamID: "s1",
				Type:    ares_events.EventLLMCall,
				Payload: tt.payload, Timestamp: base,
			})

			spans := tl.GetTrace("a1")
			require.Len(t, spans, 1)
			assert.Equal(t, "llm.call", spans[0].Name)
			assert.Equal(t, base, spans[0].StartTime)
			assert.Equal(t, tt.wantDur, spans[0].Duration)
		})
	}
}

func TestRecord_TaskCreatedCompleted(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "evt-1", StreamID: "s1",
		Type:      ares_events.EventTaskCreated,
		Payload:   map[string]any{"agent_id": "a1", "task_id": "t1"},
		Timestamp: now,
	})

	spans := tl.GetTrace("a1")
	require.Len(t, spans, 1)
	assert.Equal(t, "task.t1", spans[0].Name)
	assert.Equal(t, "ok", spans[0].Status)
	assert.True(t, spans[0].EndTime.IsZero())

	tl.Record(&ares_events.Event{
		ID: "evt-2", StreamID: "s1",
		Type:      ares_events.EventTaskCompleted,
		Payload:   map[string]any{"task_id": "t1"},
		Timestamp: now.Add(10 * time.Second),
	})

	spans = tl.GetTrace("a1")
	require.Len(t, spans, 1)
	assert.False(t, spans[0].EndTime.IsZero())
	assert.Equal(t, 10*time.Second, spans[0].Duration)
	assert.Equal(t, "ok", spans[0].Status)
}

func TestRecord_TaskFailed(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventTaskCreated,
		Payload: map[string]any{"agent_id": "a1", "task_id": "t1"}, Timestamp: now,
	})
	tl.Record(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventTaskFailed,
		Payload: map[string]any{"task_id": "t1"}, Timestamp: now.Add(5 * time.Second),
	})

	spans := tl.GetTrace("a1")
	require.Len(t, spans, 1)
	assert.Equal(t, "error", spans[0].Status)
	assert.Equal(t, 5*time.Second, spans[0].Duration)
}

func TestRecord_TaskCreatedFallbackID(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "evt-fallback", StreamID: "s1",
		Type:      ares_events.EventTaskCreated,
		Payload:   map[string]any{"agent_id": "a1"},
		Timestamp: now,
	})

	spans := tl.GetTrace("a1")
	require.Len(t, spans, 1)
	assert.Equal(t, "task.evt-fallback", spans[0].Name)
}

func TestGetTracesByAgent(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})
	tl.Record(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})
	tl.Record(&ares_events.Event{
		ID: "e3", StreamID: "s2", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a2"}, Timestamp: now,
	})

	assert.Len(t, tl.GetTracesByAgent("a1"), 2)
	assert.Len(t, tl.GetTracesByAgent("a2"), 1)
	assert.Empty(t, tl.GetTracesByAgent("nope"))
}

func TestListTraces(t *testing.T) {
	tl := NewTraceLinker()
	assert.Empty(t, tl.ListTraces())

	now := time.Now()
	tl.Record(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})
	tl.Record(&ares_events.Event{
		ID: "e2", StreamID: "s2", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a2"}, Timestamp: now,
	})

	ids := tl.ListTraces()
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, "a1")
	assert.Contains(t, ids, "a2")
}

func TestGetTrace_NotFound(t *testing.T) {
	tl := NewTraceLinker()
	assert.Nil(t, tl.GetTrace("missing"))
}

func TestGetTrace_ReturnsCopy(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})

	spans := tl.GetTrace("a1")
	require.Len(t, spans, 1)
	spans[0].Name = "mutated"

	orig := tl.GetTrace("a1")
	assert.Equal(t, "agent.start", orig[0].Name)
}

func TestConcurrentAccess_TraceLinker(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()
	var wg sync.WaitGroup

	// Writers: record events for 10 different agents.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			agentID := fmt.Sprintf("a%d", id%10)
			tl.Record(&ares_events.Event{
				ID: fmt.Sprintf("e-%d", id), StreamID: "s1",
				Type:    ares_events.EventAgentStarted,
				Payload: map[string]any{"agent_id": agentID}, Timestamp: now,
			})
		}(i)
	}

	// Readers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = tl.ListTraces()
			_ = tl.GetTrace(fmt.Sprintf("a%d", id%10))
			_ = tl.GetTracesByAgent(fmt.Sprintf("a%d", id%10))
		}(i)
	}

	wg.Wait()
	assert.Len(t, tl.ListTraces(), 10)
}

func TestRecord_MissingPayloadFields(t *testing.T) {
	tests := []struct {
		name    string
		evtType ares_events.EventType
		payload map[string]any
	}{
		{"agent started nil payload", ares_events.EventAgentStarted, nil},
		{"tool started nil payload", ares_events.EventToolCallStarted, nil},
		{"llm call nil payload", ares_events.EventLLMCall, nil},
		{"task created nil payload", ares_events.EventTaskCreated, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tl := NewTraceLinker()
			// Should not panic.
			tl.Record(&ares_events.Event{
				ID: "evt-1", StreamID: "s1",
				Type: tt.evtType, Payload: tt.payload, Timestamp: time.Now(),
			})
		})
	}
}

func TestRecord_SpanIDs(t *testing.T) {
	tl := NewTraceLinker()
	now := time.Now()

	tl.Record(&ares_events.Event{
		ID: "e1", StreamID: "s1", Type: ares_events.EventAgentStarted,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})
	tl.Record(&ares_events.Event{
		ID: "e2", StreamID: "s1", Type: ares_events.EventLLMCall,
		Payload: map[string]any{"agent_id": "a1"}, Timestamp: now,
	})

	spans := tl.GetTrace("a1")
	require.Len(t, spans, 2)
	// Span IDs should be unique.
	assert.NotEqual(t, spans[0].SpanID, spans[1].SpanID)
	assert.NotEmpty(t, spans[0].SpanID)
	assert.NotEmpty(t, spans[1].SpanID)
}
