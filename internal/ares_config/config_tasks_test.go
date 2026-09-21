package ares_config

import (
	"testing"
	"time"
)

// TestExternalTaskSurfaceDefaults locks the ares.yaml-only external entry
// defaults: server.default_capability and tasks.wait_timeout.
func TestExternalTaskSurfaceDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()

	if cfg.Server.DefaultCapability != DefaultCapabilityDefault {
		t.Fatalf("DefaultCapability = %q, want %q", cfg.Server.DefaultCapability, DefaultCapabilityDefault)
	}
	if cfg.Tasks.WaitTimeout != "60s" {
		t.Fatalf("Tasks.WaitTimeout = %q, want 60s", cfg.Tasks.WaitTimeout)
	}
	if _, err := time.ParseDuration(cfg.Tasks.WaitTimeout); err != nil {
		t.Fatalf("default wait_timeout must parse: %v", err)
	}
}

// TestValidateTasksWaitTimeout pins tasks.wait_timeout validation: empty is
// fine (defaults fill it), unparseable and non-positive are rejected.
func TestValidateTasksWaitTimeout(t *testing.T) {
	ok := &Config{Tasks: TasksConfig{WaitTimeout: "90s"}}
	if err := ok.validateTasks(); err != nil {
		t.Fatalf("valid wait_timeout rejected: %v", err)
	}

	empty := &Config{}
	if err := empty.validateTasks(); err != nil {
		t.Fatalf("empty wait_timeout must pass (setDefaults fills it): %v", err)
	}

	bad := &Config{Tasks: TasksConfig{WaitTimeout: "soon"}}
	if err := bad.validateTasks(); err == nil {
		t.Fatal("unparseable wait_timeout must be rejected")
	}

	neg := &Config{Tasks: TasksConfig{WaitTimeout: "-5s"}}
	if err := neg.validateTasks(); err == nil {
		t.Fatal("non-positive wait_timeout must be rejected")
	}
}

// TestValidateServerDefaultCapability pins the plan §2.2 validation:
// an explicitly whitespace-only server.default_capability is rejected; empty
// is legal (setDefaults backfills "ares/plan" on the production load path)
// and any non-blank value passes (the value itself is audit-only).
func TestValidateServerDefaultCapability(t *testing.T) {
	blank := &Config{Server: ServerConfig{Port: 8080, DefaultCapability: "   "}}
	if err := blank.validateServer(); err == nil {
		t.Fatal("whitespace-only default_capability must be rejected")
	}

	empty := &Config{Server: ServerConfig{Port: 8080}}
	if err := empty.validateServer(); err != nil {
		t.Fatalf("empty default_capability must pass (setDefaults fills it): %v", err)
	}

	ok := &Config{Server: ServerConfig{Port: 8080, DefaultCapability: "ares/plan"}}
	if err := ok.validateServer(); err != nil {
		t.Fatalf("valid default_capability rejected: %v", err)
	}
}
