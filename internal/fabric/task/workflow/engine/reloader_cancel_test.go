package engine

import (
	"context"
	"testing"
	"time"
)

// TestFileWatcherWatchStopsOnContextCancel locks F-17: the watch goroutine
// must honor the context passed to Watch, not only the internal stop context
// a Close() call would cancel. A caller cancelling its context without
// Close used to leave the event loop — and its fsnotify handle — alive
// forever.
func TestFileWatcherWatchStopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	w := newTestFileWatcher(t, NewJSONFileLoader(), map[string]*Workflow{})

	ctx, cancel := context.WithCancel(context.Background())
	if err := w.Watch(ctx, dir); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	cancel()

	done := make(chan struct{})
	go func() {
		w.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watch goroutine must exit on Watch-context cancellation (F-17)")
	}

	// Close must stay safe after the loop already exited on its own.
	w.Close()
}
