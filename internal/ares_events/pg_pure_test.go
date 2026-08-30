// nolint: errcheck // Test code may ignore return values
package ares_events

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildStreamReadQuery(t *testing.T) {
	q, args := buildStreamReadQuery("stream-x", ReadOptions{})
	assert.Contains(t, q, "WHERE stream_id = $1")
	assert.Equal(t, []any{"stream-x"}, args)
	assert.NotContains(t, q, "AND version")
	assert.NotContains(t, q, "created_at >=")
	assert.Contains(t, q, "ORDER BY version ASC")

	// All opt-in filters + descending + limit.
	q, args = buildStreamReadQuery("stream-x", ReadOptions{
		FromVersion: 42,
		Since:       time.Now(),
		Direction:   ReadDescending,
		Limit:       25,
	})
	assert.Contains(t, q, "AND version >= $2")
	assert.Contains(t, q, "AND created_at >= $3")
	assert.Contains(t, q, "ORDER BY version DESC")
	assert.Contains(t, q, "LIMIT $4")
	require.Len(t, args, 4)
	assert.Equal(t, int64(42), args[1])
	assert.Equal(t, 25, args[3])
}

func TestBuildAllReadQuery(t *testing.T) {
	q, args := buildAllReadQuery(ReadOptions{})
	assert.Contains(t, q, "WHERE 1=1")
	assert.Contains(t, q, "ORDER BY created_at ASC")
	assert.Empty(t, args)

	q, args = buildAllReadQuery(ReadOptions{Since: time.Now(), Direction: ReadDescending, Limit: 7})
	assert.Contains(t, q, "AND created_at >= $1")
	assert.Contains(t, q, "ORDER BY created_at DESC")
	assert.Contains(t, q, "LIMIT $2")
	require.Len(t, args, 2)
}

func TestBuildSubscribeQuery(t *testing.T) {
	cursor := time.Now()
	q, args := buildSubscribeQuery(EventFilter{}, cursor)
	assert.Contains(t, q, "created_at >= $1")
	assert.Equal(t, []any{cursor}, args)

	// Multiple streams + types.
	q, args = buildSubscribeQuery(EventFilter{
		StreamIDs: []string{"a", "b"},
		Types:     []EventType{"t1", "t2"},
		Since:     cursor,
	}, cursor)
	assert.Contains(t, q, "stream_id = ANY($2)")
	assert.Contains(t, q, "AND type = ANY($3)")
	assert.Contains(t, q, "LIMIT 100")
	require.Len(t, args, 3)
	assert.Equal(t, []string{"a", "b"}, args[1])
	assert.Equal(t, []string{"t1", "t2"}, args[2])
}

func TestPgSubscriptionMarkDelivered(t *testing.T) {
	sub := &pgSubscription{delivered: make(map[string]bool, 1024)}
	sub.markDelivered([]*Event{{ID: "e1"}, {ID: "e2"}})
	assert.True(t, sub.delivered["e1"])
	assert.True(t, sub.delivered["e2"])

	// Overflowing the bound resets the map (burst behavior), never panics.
	burst := make([]*Event, 0, maxDeliveredIDs+1)
	for i := 0; i <= maxDeliveredIDs; i++ {
		burst = append(burst, &Event{ID: "burst-" + string(rune(i))})
	}
	sub.markDelivered(burst)
	assert.False(t, sub.delivered["e1"], "overflow must reset the delivered set")
}

// fakeSummaryScanner mimics a sql.Row / pgx row for scanSummary: it returns
// pre-computed values to each Scan destination by reflection.
type fakeSummaryScanner struct{ vals []any }

func (f *fakeSummaryScanner) Scan(dest ...any) error {
	for i, d := range dest {
		if i >= len(f.vals) {
			break
		}
		v := reflect.ValueOf(d)
		if v.Kind() != reflect.Pointer {
			return errors.New("scan dest is not a pointer")
		}
		v.Elem().Set(reflect.ValueOf(f.vals[i]))
	}
	return nil
}

func TestScanSummaryFullRow(t *testing.T) {
	now := time.Now()
	scanner := &fakeSummaryScanner{vals: []any{
		"sum-1", "stream-a", "agent-1", "task-1", "session-1", "user-1",
		"did work", 5, int64(1), int64(6), now, now,
		[]byte(`{"evt.created":2}`), // event_type_counts
		[]byte(`["t1","t2"]`),       // tasks_created
		[]byte(`["search"]`),        // tools_called
		[]byte(`["boom"]`),          // errors
		"request text", "completed", []byte(`{"k":"v"}`), now,
	}}
	s, err := scanSummary(scanner)
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, "sum-1", s.ID)
	assert.Equal(t, "stream-a", s.StreamID)
	assert.Equal(t, "did work", s.SummaryText)
	assert.Equal(t, 5, s.EventCount)
	assert.Equal(t, 2, s.EventTypeCounts["evt.created"])
	assert.Equal(t, []string{"t1", "t2"}, s.TasksCreated)
	assert.Equal(t, []string{"search"}, s.ToolsCalled)
	assert.Equal(t, []string{"boom"}, s.Errors)
	assert.Equal(t, "completed", s.Outcome)
	assert.Equal(t, `{"k":"v"}`, string(s.Metadata))
}

func TestScanSummarySparseRow(t *testing.T) {
	now := time.Now()
	// Nil/empty JSON columns must be skipped, not error.
	scanner := &fakeSummaryScanner{vals: []any{
		"sum-2", "stream-a", "", "", "", "",
		"", 0, int64(0), int64(0), now, now,
		[]byte(nil), []byte(nil), []byte(nil), []byte(nil),
		"", "", []byte(nil), now,
	}}
	s, err := scanSummary(scanner)
	require.NoError(t, err)
	assert.Empty(t, s.TasksCreated)
	assert.Empty(t, s.Metadata)
}

func TestScanSummaryScanError(t *testing.T) {
	// A scanner that errors on Scan must propagate the error.
	_, err := scanSummary(errorScanner{})
	require.Error(t, err)
}

type errorScanner struct{}

func (errorScanner) Scan(dest ...any) error {
	return context.Canceled
}
