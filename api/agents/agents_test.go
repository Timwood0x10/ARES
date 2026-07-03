package agents

import (
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig("worker")
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if string(cfg.Type) != "worker" {
		t.Fatalf("expected type worker, got %s", cfg.Type)
	}
	if cfg.HeartbeatInterval <= 0 {
		t.Fatal("expected positive HeartbeatInterval")
	}
}

func TestNewLeaderReturnsNil(t *testing.T) {
	agent, err := NewLeader("test", nil, nil, nil, nil, nil, &Config{ID: "test"})
	if err != nil {
		t.Fatalf("NewLeader: %v", err)
	}
	if agent != nil {
		t.Fatal("expected nil agent (stub)")
	}
}

func TestNewSubReturnsNil(t *testing.T) {
	agent, err := NewSub("test", "worker", nil, nil, &Config{ID: "test"})
	if err != nil {
		t.Fatalf("NewSub: %v", err)
	}
	if agent != nil {
		t.Fatal("expected nil agent (stub)")
	}
}

func TestTypeAliases(t *testing.T) {
	var la LeaderAgent = nil
	var sa SubAgent = nil
	_ = la
	_ = sa
}

func TestConfigDefaults(t *testing.T) {
	cfg := DefaultConfig("worker")
	if cfg.MaxRetries <= 0 {
		t.Fatal("expected positive MaxRetries")
	}
	if cfg.Timeout <= 0 {
		t.Fatal("expected positive Timeout")
	}
}

func TestAHPMessageType(t *testing.T) {
	var msg AHPMessage
	_ = msg
}
