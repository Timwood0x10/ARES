// Package memory provides unified memory management for the StyleAgent framework.
package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

const errPrefix = "memory: "

// ── MemoryPatchExecutor ────────────────────────────────────

// MemoryPatchExecutor implements patch.RuntimeComponent for the Memory subsystem,
// enabling runtime evolution of memory configuration (history depth, session TTL,
// distilled task limits, etc.).
type MemoryPatchExecutor struct {
	mgr *ProductionMemoryManager
}

// NewMemoryPatchExecutor creates a RuntimeComponent adapter for ProductionMemoryManager.
func NewMemoryPatchExecutor(mgr *ProductionMemoryManager) *MemoryPatchExecutor {
	return &MemoryPatchExecutor{mgr: mgr}
}

// Name returns the component identifier.
func (e *MemoryPatchExecutor) Name() string { return "memory" }

// Snapshot returns the current memory config as a snapshot.
func (e *MemoryPatchExecutor) Snapshot(_ context.Context) (any, error) {
	if e.mgr == nil || e.mgr.config == nil {
		return nil, fmt.Errorf(errPrefix + "no config available")
	}
	// Return a copy to avoid mutation via the snapshot.
	cfg := *e.mgr.config
	return &cfg, nil
}

// Apply patches the memory configuration. Supported patch types:
//   - PatchChangePlanner — change max_history or max_tasks
//   - PatchChangeBudget  — change max_distilled_tasks or session_ttl
//   - PatchChangeReducer — change clean_options
func (e *MemoryPatchExecutor) Apply(ctx context.Context, p patch.RuntimePatch) (*patch.RuntimePatch, error) {
	if e.mgr == nil {
		return nil, fmt.Errorf(errPrefix + "manager is nil")
	}

	e.mgr.mu.Lock()
	defer e.mgr.mu.Unlock()

	// Build rollback = snapshot of current config before mutation.
	rollbackCfg := *e.mgr.config

	switch p.Type {
	case patch.PatchChangePlanner:
		// Value is expected as a map: {"max_history": 50, "max_tasks": 1000}
		if v, ok := p.Value.(map[string]any); ok {
			if h, ok := v["max_history"].(int); ok && h > 0 {
				e.mgr.config.MaxHistory = h
			}
			if t, ok := v["max_tasks"].(int); ok && t > 0 {
				e.mgr.config.MaxTasks = t
			}
			if s, ok := v["max_sessions"].(int); ok && s > 0 {
				e.mgr.config.MaxSessions = s
			}
		}

	case patch.PatchChangeBudget:
		// Value is expected as a map: {"max_distilled_tasks": 500, "session_ttl": "24h"}
		if v, ok := p.Value.(map[string]any); ok {
			if d, ok := v["max_distilled_tasks"].(int); ok && d > 0 {
				e.mgr.config.MaxDistilledTasks = d
			}
			if t, ok := v["session_ttl"].(string); ok && t != "" {
				dur, err := fmtDuration(t)
				if err != nil {
					return nil, fmt.Errorf(errPrefix+"invalid session_ttl: %w", err)
				}
				e.mgr.config.SessionTTL = dur
			}
		}

	case patch.PatchChangeReducer:
		// Value is expected as a map: {"use_structured_cleaning": true}
		if v, ok := p.Value.(map[string]any); ok {
			if s, ok := v["use_structured_cleaning"].(bool); ok {
				e.mgr.config.UseStructuredCleaning = s
			}
		}

	default:
		return nil, fmt.Errorf(errPrefix+"unsupported patch type %s", p.Type)
	}

	// Return rollback patch.
	return &patch.RuntimePatch{
		Type:   p.Type,
		Target: p.Target,
		Value:  rollbackCfg,
		Reason: "rollback: restore previous memory config",
	}, nil
}

// CanApply returns nil if the patch type is supported.
func (e *MemoryPatchExecutor) CanApply(_ context.Context, p patch.RuntimePatch) error {
	switch p.Type {
	case patch.PatchChangePlanner, patch.PatchChangeBudget, patch.PatchChangeReducer:
		return nil
	default:
		return fmt.Errorf(errPrefix+"unsupported patch type %s", p.Type)
	}
}

// Ensure MemoryPatchExecutor implements patch.RuntimeComponent.
var _ patch.RuntimeComponent = (*MemoryPatchExecutor)(nil)

// fmtDuration parses a duration string like "24h", "30m", "7d".
// Supports days (d) in addition to Go's standard duration units.
func fmtDuration(s string) (time.Duration, error) {
	if len(s) > 1 && s[len(s)-1] == 'd' {
		days := 0
		if _, err := fmt.Sscanf(s, "%dd", &days); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	return time.ParseDuration(s)
}
