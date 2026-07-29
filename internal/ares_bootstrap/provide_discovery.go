// Package ares_bootstrap wires discovery engine construction.
//
// provide_discovery.go constructs the optional service discovery engine that
// auto-detects MCP servers and agent runtimes. The engine is opt-in via the
// Discovery config section; when disabled, ProvideDiscovery returns nil and
// the discovery packages remain unused.
package ares_bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/discovery"
	"github.com/Timwood0x10/ares/internal/discovery/providers"
)

// DiscoveryComponents holds the wired discovery engine. It is nil when
// discovery is disabled in config.
type DiscoveryComponents struct {
	Engine *discovery.Engine
}

// ErrDiscoveryDisabled is returned by ProvideDiscovery when the discovery
// engine is disabled in configuration. Callers should check for this sentinel
// with errors.Is and treat it as a non-error no-op.
var ErrDiscoveryDisabled = fmt.Errorf("discovery disabled in config")

// ProvideDiscovery constructs the discovery engine with the default provider
// set (ARES, Claude, Cursor, VSCode configs + PATH binary probe) and starts
// auto-discovery. Returns ErrDiscoveryDisabled when cfg is nil or discovery
// is disabled, so callers can ignore the component entirely in the default
// configuration.
//
// Args:
//
//	ctx    - lifecycle context for the auto-discovery loop; cancels on shutdown.
//	cfg    - discovery configuration; nil or Enabled=false yields ErrDiscoveryDisabled.
//
// Returns:
//
//	comp   - DiscoveryComponents with a started Engine, or nil when disabled.
//	err    - non-nil only on provider construction failure (currently always
//	         nil because each provider constructor is infallible).
func ProvideDiscovery(ctx context.Context, cfg *ares_config.DiscoveryConfig) (*DiscoveryComponents, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, ErrDiscoveryDisabled
	}

	eng := discovery.NewEngine(discovery.NewMemoryStore(), nil)
	// Provider constructors vary in signature: ARES, Cursor, and the binary
	// probe take no args (they derive paths from $HOME or $PATH), while Claude
	// and VSCode take a project directory to scan for project-local config.
	eng.AddProvider(providers.NewARESProvider())
	eng.AddProvider(providers.NewClaudeProvider(cfg.ProjectDir))
	eng.AddProvider(providers.NewCursorProvider())
	eng.AddProvider(providers.NewVSCodeProvider(cfg.ProjectDir))
	eng.AddProvider(providers.NewBinaryProbeProvider())

	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	eng.StartAutoDiscovery(ctx, interval)
	return &DiscoveryComponents{Engine: eng}, nil
}
