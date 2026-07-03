// Package events tests.
package events

import (
	"context"
	"testing"

	internal "github.com/Timwood0x10/ares/internal/ares_events"
)

func TestNewInMemory(t *testing.T) {
	s := NewInMemory()
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestAppendAndRead(t *testing.T) {
	s := NewInMemory()
	ctx := context.Background()

	evt := &internal.Event{
		StreamID: "test",
		Type:     "test.event",
	}
	err := s.Append(ctx, "test", []*internal.Event{evt}, 0)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	evts, err := s.Read(ctx, "test", internal.ReadOptions{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evts))
	}
}

func TestReadAll(t *testing.T) {
	s := NewInMemory()
	ctx := context.Background()

	evts, err := s.ReadAll(ctx, internal.ReadOptions{})
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if len(evts) != 0 {
		t.Fatalf("expected 0 events, got %d", len(evts))
	}
}

func TestSubscribe(t *testing.T) {
	s := NewInMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ch, err := s.Subscribe(ctx, internal.EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_, ok := <-ch
	if ok {
		t.Fatal("expected closed channel on cancelled ctx")
	}
}
