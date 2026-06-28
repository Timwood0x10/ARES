// Package data provides state aggregation for the ARES Console monitoring plugin.
package data

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/eventutil"
)

// TraceLinker builds a span tree from events.
// All methods are safe for concurrent use.
type TraceLinker struct {
	mu          sync.RWMutex
	spans       map[string][]monitoring.TraceSpan // traceID -> spans
	openSpans   map[string]*monitoring.TraceSpan  // closeKey -> open span pointer
	spanCounter atomic.Int64
}

// NewTraceLinker creates a new TraceLinker with empty span maps.
func NewTraceLinker() *TraceLinker {
	return &TraceLinker{
		spans:     make(map[string][]monitoring.TraceSpan),
		openSpans: make(map[string]*monitoring.TraceSpan),
	}
}

// Record processes an event and creates or closes trace spans.
func (tl *TraceLinker) Record(evt *ares_events.Event) {
	if evt == nil {
		return
	}

	switch evt.Type {
	case ares_events.EventAgentStarted:
		tl.handleAgentStarted(evt)
	case ares_events.EventAgentStopped:
		tl.handleAgentStopped(evt)
	case ares_events.EventToolCallStarted:
		tl.handleToolCallStarted(evt)
	case ares_events.EventToolCallCompleted:
		tl.handleToolCallCompleted(evt)
	case ares_events.EventLLMCall:
		tl.handleLLMCall(evt)
	case ares_events.EventTaskCreated:
		tl.handleTaskCreated(evt)
	case ares_events.EventTaskCompleted:
		tl.handleTaskClosed(evt, "ok")
	case ares_events.EventTaskFailed:
		tl.handleTaskClosed(evt, "error")
	}
}

// GetTrace returns a copy of all spans for the given trace ID.
func (tl *TraceLinker) GetTrace(traceID string) []monitoring.TraceSpan {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	spans, ok := tl.spans[traceID]
	if !ok {
		return nil
	}
	cp := make([]monitoring.TraceSpan, len(spans))
	copy(cp, spans)
	return cp
}

// GetTracesByAgent returns all spans whose AgentID matches.
func (tl *TraceLinker) GetTracesByAgent(agentID string) []monitoring.TraceSpan {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	var result []monitoring.TraceSpan
	for _, spans := range tl.spans {
		for _, s := range spans {
			if s.AgentID == agentID {
				result = append(result, s)
			}
		}
	}
	return result
}

// ListTraces returns all tracked trace IDs.
func (tl *TraceLinker) ListTraces() []string {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	ids := make([]string, 0, len(tl.spans))
	for id := range tl.spans {
		ids = append(ids, id)
	}
	return ids
}

// handleAgentStarted creates an "agent.start" span.
func (tl *TraceLinker) handleAgentStarted(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	if agentID == "" {
		return
	}
	traceID := resolveTraceID(evt, agentID)
	now := resolveTimestamp(evt)

	span := monitoring.TraceSpan{
		TraceID:   traceID,
		SpanID:    tl.nextSpanID(),
		Name:      "agent.start",
		AgentID:   agentID,
		Status:    "ok",
		StartTime: now,
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.spans[traceID] = append(tl.spans[traceID], span)
	last := &tl.spans[traceID][len(tl.spans[traceID])-1]
	tl.openSpans["agent:"+agentID] = last
}

// handleAgentStopped closes the "agent.start" span.
func (tl *TraceLinker) handleAgentStopped(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	if agentID == "" {
		return
	}
	key := "agent:" + agentID
	now := resolveTimestamp(evt)

	tl.mu.Lock()
	defer tl.mu.Unlock()
	span, ok := tl.openSpans[key]
	if !ok {
		return
	}
	span.EndTime = now
	span.Duration = now.Sub(span.StartTime)
	delete(tl.openSpans, key)
}

// handleToolCallStarted creates a "tool.call.{name}" span.
func (tl *TraceLinker) handleToolCallStarted(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	traceID := resolveTraceID(evt, agentID)
	toolName := eventutil.ExtractString(evt, "tool_name")
	if toolName == "" {
		toolName = "unknown"
	}
	now := resolveTimestamp(evt)

	span := monitoring.TraceSpan{
		TraceID:   traceID,
		SpanID:    tl.nextSpanID(),
		Name:      fmt.Sprintf("tool.call.%s", toolName),
		AgentID:   agentID,
		Status:    "ok",
		StartTime: now,
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.spans[traceID] = append(tl.spans[traceID], span)
	last := &tl.spans[traceID][len(tl.spans[traceID])-1]
	tl.openSpans[toolCloseKey(agentID, toolName)] = last
}

// handleToolCallCompleted closes the matching tool span.
func (tl *TraceLinker) handleToolCallCompleted(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	toolName := eventutil.ExtractString(evt, "tool_name")
	if toolName == "" {
		toolName = "unknown"
	}
	key := toolCloseKey(agentID, toolName)
	now := resolveTimestamp(evt)

	tl.mu.Lock()
	defer tl.mu.Unlock()
	span, ok := tl.openSpans[key]
	if !ok {
		return
	}
	span.EndTime = now
	span.Duration = now.Sub(span.StartTime)
	delete(tl.openSpans, key)
}

// handleLLMCall creates a complete "llm.call" span with both start and end time.
func (tl *TraceLinker) handleLLMCall(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	traceID := resolveTraceID(evt, agentID)
	now := resolveTimestamp(evt)
	dur := eventutil.ExtractDuration(evt, "duration")

	span := monitoring.TraceSpan{
		TraceID:   traceID,
		SpanID:    tl.nextSpanID(),
		Name:      "llm.call",
		AgentID:   agentID,
		Status:    "ok",
		StartTime: now,
		EndTime:   now.Add(dur),
		Duration:  dur,
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.spans[traceID] = append(tl.spans[traceID], span)
}

// handleTaskCreated creates a "task.{id}" span.
func (tl *TraceLinker) handleTaskCreated(evt *ares_events.Event) {
	agentID := eventutil.ExtractAgentID(evt)
	traceID := resolveTraceID(evt, agentID)
	taskID := eventutil.ExtractString(evt, "task_id")
	if taskID == "" {
		taskID = evt.ID
	}
	now := resolveTimestamp(evt)

	span := monitoring.TraceSpan{
		TraceID:   traceID,
		SpanID:    tl.nextSpanID(),
		Name:      fmt.Sprintf("task.%s", taskID),
		AgentID:   agentID,
		Status:    "ok",
		StartTime: now,
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.spans[traceID] = append(tl.spans[traceID], span)
	last := &tl.spans[traceID][len(tl.spans[traceID])-1]
	tl.openSpans["task:"+taskID] = last
}

// handleTaskClosed closes the matching task span with the given status.
func (tl *TraceLinker) handleTaskClosed(evt *ares_events.Event, status string) {
	taskID := eventutil.ExtractString(evt, "task_id")
	if taskID == "" {
		taskID = evt.ID
	}
	key := "task:" + taskID
	now := resolveTimestamp(evt)

	tl.mu.Lock()
	defer tl.mu.Unlock()
	span, ok := tl.openSpans[key]
	if !ok {
		return
	}
	span.EndTime = now
	span.Duration = now.Sub(span.StartTime)
	span.Status = status
	delete(tl.openSpans, key)
}

// resolveTraceID returns trace_id from payload, falling back to agentID or event ID.
func resolveTraceID(evt *ares_events.Event, agentID string) string {
	if traceID := eventutil.ExtractString(evt, "trace_id"); traceID != "" {
		return traceID
	}
	if agentID != "" {
		return agentID
	}
	return evt.ID
}

// resolveTimestamp returns the event timestamp or now if zero.
func resolveTimestamp(evt *ares_events.Event) time.Time {
	if !evt.Timestamp.IsZero() {
		return evt.Timestamp
	}
	return time.Now()
}

// nextSpanID returns a unique span identifier scoped to this TraceLinker instance.
func (tl *TraceLinker) nextSpanID() string {
	return fmt.Sprintf("span-%d", tl.spanCounter.Add(1))
}

// toolCloseKey builds the open-span map key for a tool call.
func toolCloseKey(agentID, toolName string) string {
	return fmt.Sprintf("tool:%s:%s", agentID, toolName)
}
