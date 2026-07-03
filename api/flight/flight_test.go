package flight

import (
	"context"
	"testing"
	"time"
)

func TestNewWithEmptyConfig(t *testing.T) {
	fr := New(Config{})
	if fr == nil {
		t.Fatal("expected non-nil FlightRecorder")
	}
}

func TestStartStop(t *testing.T) {
	fr := New(Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := fr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	fr.Stop()
}

func TestTimeline(t *testing.T) {
	fr := New(Config{})
	tl := fr.Timeline()
	if tl == nil {
		t.Fatal("expected non-nil Timeline")
	}
}

func TestGraphNonNull(t *testing.T) {
	fr := New(Config{})
	g := fr.Graph()
	if g == nil {
		t.Fatal("expected non-nil Graph")
	}
}

func TestDiagnosticsNonNull(t *testing.T) {
	fr := New(Config{})
	d := fr.Diagnostics()
	if d == nil {
		t.Fatal("expected non-nil Diagnostics")
	}
}

func TestConfig(t *testing.T) {
	cfg := Config{}
	if cfg.EventStore != nil {
		t.Fatal("expected nil EventStore by default")
	}
}
