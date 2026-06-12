package events

import (
	"context"
	"fmt"
	"strconv"
	"testing"
)

// BenchmarkMemoryStore_Append measures single-event append throughput.
func BenchmarkMemoryStore_Append(b *testing.B) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		streamID := "stream-" + strconv.Itoa(i%100)
		_ = store.Append(ctx, streamID, []*Event{
			{Type: EventAgentStarted, Payload: map[string]any{"idx": i}},
		}, 0)
	}
}

// BenchmarkMemoryStore_AppendBatch measures batch append of 100 events.
func BenchmarkMemoryStore_AppendBatch(b *testing.B) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Pre-build a batch of 100 events to avoid allocation overhead in the loop.
	batch := make([]*Event, 100)
	for i := range batch {
		batch[i] = &Event{
			Type:    EventTaskCreated,
			Payload: map[string]any{"index": i},
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		streamID := "batch-stream-" + strconv.Itoa(i%10)
		_ = store.Append(ctx, streamID, batch, 0)
	}
}

// BenchmarkMemoryStore_Read measures read performance on a stream with 1000 events.
func BenchmarkMemoryStore_Read(b *testing.B) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Pre-populate stream with 1000 events.
	events := make([]*Event, 1000)
	for i := range events {
		events[i] = &Event{
			Type:    EventTaskCreated,
			Payload: map[string]any{"index": i},
		}
	}
	if err := store.Append(ctx, "read-stream", events, 0); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = store.Read(ctx, "read-stream", ReadOptions{})
	}
}

// BenchmarkMemoryStore_ReadAll measures cross-stream read performance with 10000 events across 10 streams.
func BenchmarkMemoryStore_ReadAll(b *testing.B) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	// Pre-populate 10 streams with 1000 events each.
	for s := 0; s < 10; s++ {
		streamID := fmt.Sprintf("stream-%d", s)
		events := make([]*Event, 1000)
		for i := range events {
			events[i] = &Event{
				Type:    EventTaskCreated,
				Payload: map[string]any{"stream": s, "index": i},
			}
		}
		if err := store.Append(ctx, streamID, events, 0); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = store.ReadAll(ctx, ReadOptions{})
	}
}

// BenchmarkMemoryStore_Subscribe measures the cost of creating 100 subscribers.
func BenchmarkMemoryStore_Subscribe(b *testing.B) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		// Create 100 subscribers per iteration.
		for j := 0; j < 100; j++ {
			filter := EventFilter{
				Types: []EventType{EventTaskCreated},
			}
			_, _ = store.Subscribe(ctx, filter)
		}
	}
}

// BenchmarkMemoryStore_ConcurrentAppend measures concurrent append throughput with 10 goroutines.
func BenchmarkMemoryStore_ConcurrentAppend(b *testing.B) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			streamID := "concurrent-stream-" + strconv.Itoa(i%50)
			_ = store.Append(ctx, streamID, []*Event{
				{Type: EventAgentStarted, Payload: map[string]any{"idx": i}},
			}, 0)
			i++
		}
	})
}
