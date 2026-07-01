package eventutil

import (
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/require"
)

func TestExtractString(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		require.Equal(t, "", ExtractString(nil, "key"))
	})
	t.Run("nil payload", func(t *testing.T) {
		evt := &ares_events.Event{}
		require.Equal(t, "", ExtractString(evt, "key"))
	})
	t.Run("missing key", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{}}
		require.Equal(t, "", ExtractString(evt, "key"))
	})
	t.Run("wrong type", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": 42}}
		require.Equal(t, "", ExtractString(evt, "key"))
	})
	t.Run("valid string", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": "value"}}
		require.Equal(t, "value", ExtractString(evt, "key"))
	})
}

func TestExtractInt64(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		require.Equal(t, int64(0), ExtractInt64(nil, "key"))
	})
	t.Run("missing key", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{}}
		require.Equal(t, int64(0), ExtractInt64(evt, "key"))
	})
	t.Run("int64 value", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": int64(42)}}
		require.Equal(t, int64(42), ExtractInt64(evt, "key"))
	})
	t.Run("float64 value", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": float64(42.5)}}
		require.Equal(t, int64(42), ExtractInt64(evt, "key"))
	})
	t.Run("int value", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": 42}}
		require.Equal(t, int64(42), ExtractInt64(evt, "key"))
	})
	t.Run("wrong type", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": "string"}}
		require.Equal(t, int64(0), ExtractInt64(evt, "key"))
	})
}

func TestExtractFloat64(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		require.Equal(t, float64(0), ExtractFloat64(nil, "key"))
	})
	t.Run("float64 value", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": float64(3.14)}}
		require.Equal(t, float64(3.14), ExtractFloat64(evt, "key"))
	})
	t.Run("int64 value", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": int64(42)}}
		require.Equal(t, float64(42), ExtractFloat64(evt, "key"))
	})
	t.Run("int value", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": 7}}
		require.Equal(t, float64(7), ExtractFloat64(evt, "key"))
	})
	t.Run("wrong type", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": "text"}}
		require.Equal(t, float64(0), ExtractFloat64(evt, "key"))
	})
}

func TestExtractDuration(t *testing.T) {
	t.Run("nil event", func(t *testing.T) {
		require.Equal(t, time.Duration(0), ExtractDuration(nil, "key"))
	})
	t.Run("time.Duration value", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": time.Second}}
		require.Equal(t, time.Second, ExtractDuration(evt, "key"))
	})
	t.Run("float64 nanoseconds", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": float64(1e9)}}
		require.Equal(t, time.Second, ExtractDuration(evt, "key"))
	})
	t.Run("int64 nanoseconds", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": int64(2e9)}}
		require.Equal(t, 2*time.Second, ExtractDuration(evt, "key"))
	})
	t.Run("wrong type", func(t *testing.T) {
		evt := &ares_events.Event{Payload: map[string]any{"key": true}}
		require.Equal(t, time.Duration(0), ExtractDuration(evt, "key"))
	})
}

func TestExtractMapString(t *testing.T) {
	require.Equal(t, "", ExtractMapString(nil, "key"))
	require.Equal(t, "", ExtractMapString(map[string]any{}, "key"))
	require.Equal(t, "", ExtractMapString(map[string]any{"key": 42}, "key"))
	require.Equal(t, "val", ExtractMapString(map[string]any{"key": "val"}, "key"))
}

func TestExtractMapInt64(t *testing.T) {
	require.Equal(t, int64(0), ExtractMapInt64(nil, "key"))
	m := map[string]any{"a": int64(10), "b": float64(20.5), "c": 30, "d": "x"}
	require.Equal(t, int64(10), ExtractMapInt64(m, "a"))
	require.Equal(t, int64(20), ExtractMapInt64(m, "b"))
	require.Equal(t, int64(30), ExtractMapInt64(m, "c"))
	require.Equal(t, int64(0), ExtractMapInt64(m, "d"))
	require.Equal(t, int64(0), ExtractMapInt64(m, "missing"))
}

func TestExtractMapFloat64(t *testing.T) {
	require.Equal(t, float64(0), ExtractMapFloat64(nil, "key"))
	m := map[string]any{"a": float64(3.14), "b": int64(42), "c": 7, "d": "x"}
	require.InDelta(t, 3.14, ExtractMapFloat64(m, "a"), 1e-9)
	require.Equal(t, float64(42), ExtractMapFloat64(m, "b"))
	require.Equal(t, float64(7), ExtractMapFloat64(m, "c"))
	require.Equal(t, float64(0), ExtractMapFloat64(m, "d"))
}

func TestExtractAgentID(t *testing.T) {
	t.Run("agent_id in payload", func(t *testing.T) {
		evt := &ares_events.Event{StreamID: "stream1", Payload: map[string]any{"agent_id": "agent1"}}
		require.Equal(t, "agent1", ExtractAgentID(evt))
	})
	t.Run("fallback to StreamID", func(t *testing.T) {
		evt := &ares_events.Event{StreamID: "stream1"}
		require.Equal(t, "stream1", ExtractAgentID(evt))
	})
	t.Run("empty agent_id fallback to StreamID", func(t *testing.T) {
		evt := &ares_events.Event{StreamID: "stream1", Payload: map[string]any{"agent_id": ""}}
		require.Equal(t, "stream1", ExtractAgentID(evt))
	})
}
