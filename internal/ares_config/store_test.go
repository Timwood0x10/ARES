package ares_config

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// minimalYAML is a valid, minimal config the store can reload.
const storeYAML = "server:\n  port: 8080\nllm:\n  provider: openai\n  base_url: https://api.example.com/v1\n  api_key: sk-test\n  model: gpt-4\n"

func TestConfigStoreCurrent(t *testing.T) {
	initial := &Config{Server: ServerConfig{Port: 8080}}
	s := NewConfigStore(initial)
	if got := s.Current(); got.Server.Port != 8080 {
		t.Fatalf("Current().Port = %d, want 8080", got.Server.Port)
	}
	// Current returns the internal pointer (read-only contract).
	if s.Current() != initial {
		t.Fatal("Current must return the seeded config")
	}
}

func TestConfigStoreReloadSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ares.yaml")
	if err := os.WriteFile(path, []byte(storeYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewConfigStore(&Config{Server: ServerConfig{Port: 1}})
	if err := s.Reload(context.Background(), path); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := s.Current().Server.Port; got != 8080 {
		t.Fatalf("Port = %d, want 8080 after reload", got)
	}
	hist := s.History()
	if len(hist) != 1 || !hist[0].OK {
		t.Fatalf("history = %+v, want 1 successful entry", hist)
	}
}

func TestConfigStoreReloadFailureKeepsOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ares.yaml")
	if err := os.WriteFile(path, []byte(storeYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewConfigStore(&Config{Server: ServerConfig{Port: 1}})
	// Break the file with invalid YAML, then reload must fail and keep old.
	if err := os.WriteFile(path, []byte("server: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(context.Background(), path); err == nil {
		t.Fatal("Reload must fail on invalid config")
	}
	if got := s.Current().Server.Port; got != 1 {
		t.Fatalf("Port = %d, want old 1 kept after failed reload", got)
	}
	hist := s.History()
	if len(hist) != 1 || hist[0].OK {
		t.Fatalf("history = %+v, want 1 failed entry", hist)
	}
}

func TestConfigStoreHistoryBounded(t *testing.T) {
	s := NewConfigStore(&Config{})
	for i := 0; i < 30; i++ {
		s.record(i%2 == 0, "entry")
	}
	if got := len(s.History()); got != 20 {
		t.Fatalf("history length = %d, want bounded to 20", got)
	}
}

func TestConfigStoreWatchReloadsOnWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ares.yaml")
	if err := os.WriteFile(path, []byte(storeYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewConfigStore(&Config{Server: ServerConfig{Port: 1}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The watcher must be attached before we rewrite the file; otherwise the
	// first Write event races goroutine startup and is lost. Watch blocks, so
	// we give it a short settle window instead of a fragile sleep-forever.
	go func() { _ = s.Watch(ctx, path) }()
	time.Sleep(150 * time.Millisecond)

	// Rewrite the file with a different port; the watcher must pick it up.
	newYAML := "server:\n  port: 9999\nllm:\n  provider: openai\n  base_url: https://api.example.com/v1\n  api_key: sk-test\n  model: gpt-4\n"
	if err := os.WriteFile(path, []byte(newYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for s.Current().Server.Port != 9999 {
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not reload within 3s, port=%d", s.Current().Server.Port)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestConfigStoreWatchDebounceFastWrites verifies a burst of rapid writes (as
// editors produce on atomic-save) still results in a reload. Regression for
// the debounce bug where Stop()==true skipped Reset and the last event was
// silently dropped.
func TestConfigStoreWatchDebounceFastWrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ares.yaml")
	if err := os.WriteFile(path, []byte(storeYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewConfigStore(&Config{Server: ServerConfig{Port: 1}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Watch(ctx, path) }()
	time.Sleep(150 * time.Millisecond)

	// Burst: three rapid writes inside the 200ms debounce window.
	for _, port := range []int{7000, 8000, 9999} {
		yaml := "server:\n  port: " + strconv.Itoa(port) + "\nllm:\n  provider: openai\n  base_url: https://api.example.com/v1\n  api_key: sk-test\n  model: gpt-4\n"
		if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}

	// The final value must win after the debounce settles.
	deadline := time.Now().Add(3 * time.Second)
	for s.Current().Server.Port != 9999 {
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not reload to final port within 3s, port=%d", s.Current().Server.Port)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
