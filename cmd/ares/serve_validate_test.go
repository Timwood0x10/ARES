package main

import (
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_config"
)

// TestValidateServeConfig_RejectsNil locks the documented nil guard.
func TestValidateServeConfig_RejectsNil(t *testing.T) {
	if err := validateServeConfig(nil); err == nil {
		t.Fatal("nil config must be rejected")
	}
}

// TestValidateServeConfig_ValidatesTheConfig locks that the serve entry
// point actually validates what it was handed.
//
// Regression: validateServeConfig only nil-checked, while serve_wiring.go
// asserted in a comment that it "has already enforced that the full
// agent-serving entry point has its required Memory component". The
// assertion was false, and — worse — the no-config startup path
// (`ares serve --llm-url …`) returns a config from loadServeConfig without
// ever going through Config.Load, so nothing validated it at all. A config
// that fails its own validator now fails at the serve boundary with a
// readable message instead of surfacing later inside a wiring step.
func TestValidateServeConfig_ValidatesTheConfig(t *testing.T) {
	broken := ares_config.NewMinimalConfig("http://localhost:11434", "", "llama3.2")
	// Make it structurally invalid in a way Validate must catch: a negative
	// port is rejected by validateServer.
	broken.Server.Port = -1

	err := validateServeConfig(broken)
	if err == nil {
		t.Fatal("an invalid config must be rejected by the serve entry point")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Fatalf("the rejection must name the offending field, got %v", err)
	}
}

// TestValidateServeConfig_AcceptsMinimalConfig locks that the documented
// no-config startup path is not broken by the validation it now goes
// through.
func TestValidateServeConfig_AcceptsMinimalConfig(t *testing.T) {
	cfg := ares_config.NewMinimalConfig("http://localhost:11434", "", "llama3.2")
	if err := validateServeConfig(cfg); err != nil {
		t.Fatalf("the minimal serve config must be accepted, got %v", err)
	}
}
