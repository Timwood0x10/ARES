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
