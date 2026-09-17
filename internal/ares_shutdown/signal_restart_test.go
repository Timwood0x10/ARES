package ares_shutdown

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestSignalHandler_StopThenRestartIsUsable is the restart race regression:
// the old handleSignals exit defer set started=false unconditionally, so a
// Stop→Start sequence raced the OLD goroutine's defer clobbering the NEW
// Start's started=true — a subsequent Stop then returned early and the new
// signal loop leaked. After Stop→Start the handler must report started, and
// a second Stop must actually stop the new loop (observable via the started
// flag flipping to false and Start succeeding again).
func TestSignalHandler_StopThenRestartIsUsable(t *testing.T) {
	manager := NewManager(10 * time.Second)
	handler := NewSignalHandler(manager)

	if err := handler.Start(context.Background()); err != nil {
		t.Fatalf("first start: %v", err)
	}
	if err := handler.Stop(); err != nil {
		t.Fatalf("first stop: %v", err)
	}

	// Restart immediately: under the bug the old loop's deferred
	// started=false could land after this Start set started=true.
	if err := handler.Start(context.Background()); err != nil {
		t.Fatalf("restart: %v", err)
	}

	// Give any leaked old-loop defer a window to clobber the flag.
	time.Sleep(50 * time.Millisecond)

	handler.mu.RLock()
	started := handler.mu.started
	handler.mu.RUnlock()
	if !started {
		t.Fatal("restart's started flag was clobbered by the old loop's exit defer")
	}

	// The second Stop must take effect: started flips to false.
	if err := handler.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	handler.mu.RLock()
	started = handler.mu.started
	handler.mu.RUnlock()
	if started {
		t.Fatal("second stop did not clear started (Stop raced the restart)")
	}
}

// TestSignalHandler_ConcurrentStopAndRestart exercises the Stop/Start
// interleaving under -race: many rapid restart cycles must never leave the
// handler in a torn state (started=true with a dead loop, or started=false
// with a live one).
func TestSignalHandler_ConcurrentStopAndRestart(t *testing.T) {
	manager := NewManager(10 * time.Second)
	handler := NewSignalHandler(manager)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_ = handler.Start(context.Background())
				_ = handler.Stop()
			}
		}()
	}
	wg.Wait()

	handler.mu.RLock()
	started := handler.mu.started
	handler.mu.RUnlock()
	if started {
		t.Fatal("handler still marked started after all Stop cycles (leaked loop)")
	}
}
