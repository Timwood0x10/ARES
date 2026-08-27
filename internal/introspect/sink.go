package introspect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// Feed severity levels (dot color in the activity strip) and event kinds.
const (
	feedOK     = "ok"
	feedInfo   = "info"
	feedWarn   = "warn"
	feedDanger = "danger"

	kindAgent    = "agent"
	kindTask     = "task"
	kindRecovery = "recovery"
)

// Sink tails the shared EventStore and distills lifecycle events into the
// panel's activity feed — the "who died, who took work" strip (#panel
// feedback). It is strictly read-only: one Subscribe, no publishes.
//
// The intelligence engine (health/anomalies/insights) is fed by a separate
// subscription in cmd/ares (setupServeControlPlane) rather than here, keeping
// the two consumers independent (monitoring.md Phase 4).
type Sink struct {
	store *Store
}

// NewSink builds a sink feeding the given store.
func NewSink(store *Store) *Sink {
	return &Sink{store: store}
}

// Run subscribes and maps events until ctx is cancelled. Intended for
// Components.GoBackground.
func (s *Sink) Run(ctx context.Context, eventStore ares_events.EventStore) error {
	if eventStore == nil {
		return nil
	}
	ch, err := eventStore.Subscribe(ctx, ares_events.EventFilter{})
	if err != nil {
		return fmt.Errorf("introspect: subscribe: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case evt := <-ch:
			if entry, ok := MapTimelineEvent(evt); ok {
				s.store.PushEvent(entry)
			}
		}
	}
}

// MapTimelineEvent distills one bus event into a feed row. Returns false for
// event types the feed does not surface (noise control: the feed tells the
// lifecycle story, not every internal tick).
func MapTimelineEvent(evt *ares_events.Event) (TimelineEntry, bool) {
	if evt == nil {
		return TimelineEntry{}, false
	}
	e := TimelineEntry{
		TS:      evt.Timestamp,
		Type:    string(evt.Type),
		AgentID: str(evt.Payload["agent_id"]),
		TaskID:  str(evt.Payload["task_id"]),
	}
	switch evt.Type {
	case ares_events.EventAgentStopped:
		e.Kind, e.Level = kindAgent, feedDanger
		reason := str(evt.Payload["reason"])
		e.Text = fmt.Sprintf("%s died", idOr(e.AgentID, "agent"))
		if reason != "" {
			e.Text += " (" + reason + ")"
		}
	case ares_events.EventStepRecoveryCompleted:
		e.Kind, e.Level = kindRecovery, feedOK
		e.Text = fmt.Sprintf("recovered %s", idOr(e.TaskID, e.AgentID, "work"))
	case ares_events.EventStepRecoveryStarted:
		e.Kind, e.Level = kindRecovery, feedWarn
		e.Text = "recovery started"
	case ares_events.EventStepRecoveryFailed:
		e.Kind, e.Level = feedDanger, feedDanger
		e.Text = "recovery FAILED"
	case ares_events.EventTaskCreated, ares_events.EventTaskReady:
		e.Kind, e.Level = kindTask, feedInfo
		e.Text = fmt.Sprintf("%s ready", idOr(e.TaskID, "task"))
	case ares_events.EventTaskAcquired, ares_events.EventTaskDispatched, ares_events.EventTaskStarted:
		e.Kind, e.Level = kindTask, feedInfo
		e.Text = fmt.Sprintf("%s → %s", idOr(e.TaskID, "task"), idOr(e.AgentID, "?"))
	case ares_events.EventTaskYielded, ares_events.EventTaskCheckpointed:
		e.Kind, e.Level = kindTask, feedInfo
		e.Text = fmt.Sprintf("%s yielded at quantum", idOr(e.TaskID, "task"))
	case ares_events.EventTaskPreempted, ares_events.EventTaskStolen, ares_events.EventTaskExpired:
		e.Kind, e.Level = kindTask, feedWarn
		e.Text = fmt.Sprintf("%s preempted", idOr(e.TaskID, "task"))
	case ares_events.EventTaskReleased:
		e.Kind, e.Level = kindTask, feedInfo
		e.Text = fmt.Sprintf("%s released", idOr(e.TaskID, "task"))
	case ares_events.EventTaskCompleted:
		e.Kind, e.Level = kindTask, feedOK
		e.Text = fmt.Sprintf("%s done", idOr(e.TaskID, "task"))
	case ares_events.EventTaskFailed:
		e.Kind, e.Level = feedDanger, feedDanger
		e.Text = fmt.Sprintf("%s failed", idOr(e.TaskID, "task"))
	default:
		return TimelineEntry{}, false
	}
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	return e, true
}

func str(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func idOr(ids ...string) string {
	for _, s := range ids {
		if s != "" && s != "?" {
			return s
		}
	}
	if len(ids) > 0 {
		return ids[len(ids)-1]
	}
	return ""
}
