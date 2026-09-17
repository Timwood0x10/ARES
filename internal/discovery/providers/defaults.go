package providers

import (
	"github.com/Timwood0x10/ares/internal/discovery"
)

// NewDefaultEngine builds a discovery engine over the given store (nil falls
// back to an in-memory store) with the default provider set: the ARES, Claude,
// Cursor, and VSCode config scans plus the PATH binary probe. projectDir
// scopes the Claude/VSCode project-level config scans ("" scans user-level
// config only). A nil health checker skips background health probes.
//
// This is the convenience assembly the bootstrap wiring and the discovery
// fixtures share — it replaced the discoveryapi forwarding layer's
// NewEngine(EngineConfig) wrapper when that layer was removed.
func NewDefaultEngine(projectDir string, store discovery.ServiceStore, health discovery.HealthChecker) *discovery.Engine {
	if store == nil {
		store = discovery.NewMemoryStore()
	}
	eng := discovery.NewEngine(store, health)
	eng.AddProvider(NewARESProvider())
	eng.AddProvider(NewClaudeProvider(projectDir))
	eng.AddProvider(NewCursorProvider())
	eng.AddProvider(NewVSCodeProvider(projectDir))
	eng.AddProvider(NewBinaryProbeProvider())
	return eng
}
