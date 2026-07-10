// Package events tests.
package events

import (
	"context"
	"testing"
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

	evt := &Event{
		Type: "test.event",
	}
	err := s.Append(ctx, "test", []*Event{evt}, 0)
	if err != nil {
		t.Fatalf("append: %v", err)
	}

	evts, err := s.Read(ctx, "test", ReadOptions{})
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

	evts, err := s.ReadAll(ctx, ReadOptions{})
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

	ch, err := s.Subscribe(ctx, EventFilter{})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	_, ok := <-ch
	if ok {
		t.Fatal("expected closed channel on cancelled ctx")
	}
}
