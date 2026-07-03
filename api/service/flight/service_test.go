// Package flight tests.
package flight

import (
	"context"
	"testing"
)

func TestNewRecorder(t *testing.T) {
	r := New(nil)
	if r == nil {
		t.Fatal("expected non-nil recorder")
	}
}

func TestRecorderLifecycle(t *testing.T) {
	r := New(nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	r.Stop()
}

func TestRecorderTimeline(t *testing.T) {
	r := New(nil)
	tl := r.Timeline()
	if tl == nil {
		t.Fatal("expected non-nil timeline")
	}
}

func TestRecorderDiagnostics(t *testing.T) {
	r := New(nil)
	d := r.Diagnostics()
	if d == nil {
		t.Fatal("expected non-nil diagnostics")
	}
}
