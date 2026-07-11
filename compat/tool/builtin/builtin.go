// Package builtin holds the official builtin tool adapters for ARES.
//
// ARES maintains a minimal builtin set (filesystem, shell, http); third-party
// tools register via compat.RegisterTool. This skeleton package exists so the
// tool/ builtin/ directory structure matches next_step.md; the real tools
// will be wired incrementally.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/compat/tool"
)

// Noop is a placeholder builtin tool that returns its args unchanged.
// It exists so compat.RegisterTool can be smoke-tested without a real binding.
type Noop struct{}

// New constructs a Noop from a raw config map (currently unused).
func New(_ map[string]any) (tool.Tool, error) { return &Noop{}, nil }

// Execute returns the args as the result, unchanged.
func (*Noop) Execute(_ context.Context, args json.RawMessage) (any, error) {
	if len(args) == 0 {
		return nil, errors.New("noop: args must not be empty")
	}
	return string(args), nil
}

// Name returns the canonical tool name.
func (*Noop) Name() string { return "builtin.noop" }

// Description returns a human-readable summary.
func (*Noop) Description() string {
	return "Noop placeholder tool that echoes its args; for骨架 wiring only."
}

// Compile-time interface assertion.
var _ tool.Tool = (*Noop)(nil)

// ensure fmt stays used if future stubs add error formatting.
var _ = fmt.Sprintf
