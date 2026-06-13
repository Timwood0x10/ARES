// Package flight provides runtime intelligence for GoAgentX agents.
// It records execution timelines, call graphs, decisions, memory pipelines,
// and diagnostics — the "flight recorder" for multi-agent systems.
package flight

import (
	"sync"
	"time"
)

// EventType classifies a timeline event.
type EventType string

const (
	EventAgentStart EventType = "agent.start"
	EventAgentEnd   EventType = "agent.end"
	EventToolCall   EventType = "tool.call"
	EventToolResult EventType = "tool.result"
	EventLLMCall    EventType = "llm.call"
	EventLLMResult  EventType = "llm.result"
	EventWaiting    EventType = "waiting"
	EventError      EventType = "error"
	EventMemoryOp   EventType = "memory.op"
	EventDecision   EventType = "decision"
)

// TimelineEvent represents a single event in an agent's execution timeline.
type TimelineEvent struct {
	ID       string         `json:"id"`
	ParentID string         `json:"parent_id,omitempty"`
	AgentID  string         `json:"agent_id"`
	Type     EventType      `json:"type"`
	Name     string         `json:"name"`
	StartAt  time.Time      `json:"start_at"`
	EndAt    time.Time      `json:"end_at,omitempty"`
	Duration time.Duration  `json:"duration"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// TimelineSummary aggregates time distribution.
type TimelineSummary struct {
	TotalDuration time.Duration `json:"total_duration"`
	ToolDuration  time.Duration `json:"tool_duration"`
	LLMDuration   time.Duration `json:"llm_duration"`
	WaitDuration  time.Duration `json:"wait_duration"`
	ErrorDuration time.Duration `json:"error_duration"`
	ToolPercent   float64       `json:"tool_percent"`
	LLMPercent    float64       `json:"llm_percent"`
	WaitPercent   float64       `json:"wait_percent"`
	EventCount    int           `json:"event_count"`
}

// Timeline holds ordered events for one or more agents.
type Timeline struct {
	events []TimelineEvent
	mu     sync.RWMutex
}

// NewTimeline creates an empty timeline.
func NewTimeline() *Timeline {
	return &Timeline{
		events: make([]TimelineEvent, 0, 64),
	}
}

// Add appends an event to the timeline.
func (t *Timeline) Add(event TimelineEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

// Events returns a copy of all events.
func (t *Timeline) Events() []TimelineEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]TimelineEvent, len(t.events))
	copy(result, t.events)
	return result
}

// FilterByAgent returns events for a specific agent.
func (t *Timeline) FilterByAgent(agentID string) []TimelineEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result []TimelineEvent
	for _, e := range t.events {
		if e.AgentID == agentID {
			result = append(result, e)
		}
	}
	return result
}

// FilterByType returns events of a specific type.
func (t *Timeline) FilterByType(eventType EventType) []TimelineEvent {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var result []TimelineEvent
	for _, e := range t.events {
		if e.Type == eventType {
			result = append(result, e)
		}
	}
	return result
}

// Summary computes time distribution across event types.
func (t *Timeline) Summary() TimelineSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var summary TimelineSummary
	summary.EventCount = len(t.events)

	for _, e := range t.events {
		summary.ToolDuration += typeDuration(e, EventToolCall, EventToolResult)
		summary.LLMDuration += typeDuration(e, EventLLMCall, EventLLMResult)
		summary.WaitDuration += typeDuration(e, EventWaiting)
		summary.ErrorDuration += typeDuration(e, EventError)
	}

	// Total = max(end) - min(start).
	if len(t.events) > 0 {
		minStart := t.events[0].StartAt
		maxEnd := t.events[0].EndAt
		for _, e := range t.events {
			if e.StartAt.Before(minStart) {
				minStart = e.StartAt
			}
			if e.EndAt.After(maxEnd) {
				maxEnd = e.EndAt
			}
		}
		if !maxEnd.IsZero() && maxEnd.After(minStart) {
			summary.TotalDuration = maxEnd.Sub(minStart)
		}
	}

	if summary.TotalDuration > 0 {
		summary.ToolPercent = float64(summary.ToolDuration) / float64(summary.TotalDuration) * 100
		summary.LLMPercent = float64(summary.LLMDuration) / float64(summary.TotalDuration) * 100
		summary.WaitPercent = float64(summary.WaitDuration) / float64(summary.TotalDuration) * 100
	}

	return summary
}

// Len returns the number of events.
func (t *Timeline) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.events)
}

// typeDuration returns the event's duration if its type matches any of the given types.
func typeDuration(e TimelineEvent, types ...EventType) time.Duration {
	for _, tp := range types {
		if e.Type == tp {
			return e.Duration
		}
	}
	return 0
}
