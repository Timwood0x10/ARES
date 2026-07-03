// Package dashboard — arena bridge.
//
// ArenaBridge adapts internal/ares_arena.Service to the dashboard's
// ArenaProvider and SurvivalProvider interfaces, providing real chaos
// engineering operations through the dashboard API.
package dashboard

import (
	"context"
	"sync"

	ares_arena "github.com/Timwood0x10/ares/internal/ares_arena"
)

var _ ArenaProvider = (*ArenaBridge)(nil)

// ArenaBridge wraps ares_arena.Service to implement ArenaProvider.
type ArenaBridge struct {
	svc *ares_arena.Service
	ctx context.Context

	mu           sync.Mutex
	totalActions int
	hist         []ArenaResult
}

// NewArenaBridge creates an ArenaBridge backed by the real arena service.
func NewArenaBridge(ctx context.Context, svc *ares_arena.Service) *ArenaBridge {
	return &ArenaBridge{
		svc:  svc,
		ctx:  ctx,
		hist: make([]ArenaResult, 0, 64),
	}
}

// Execute runs a chaos action through the real arena service.
func (b *ArenaBridge) Execute(action ArenaAction) ArenaResult {
	ia := ares_arena.Action{
		Type:     ares_arena.ActionType(action.Type),
		TargetID: action.TargetID,
		SourceID: action.SourceID,
		Metadata: action.Metadata,
	}
	r := b.svc.Execute(b.ctx, ia)

	result := ArenaResult{
		Success:  r.Success,
		Error:    r.Error,
		Duration: r.Duration,
		Action:   action,
	}

	b.mu.Lock()
	b.totalActions++
	b.hist = append(b.hist, result)
	b.mu.Unlock()

	return result
}

// Stats returns aggregate arena statistics.
func (b *ArenaBridge) Stats() map[string]any {
	st := b.svc.Stats()
	b.mu.Lock()
	total := b.totalActions
	b.mu.Unlock()

	return map[string]any{
		"total_actions":       total,
		"total_internal":      st.TotalActions,
		"successful_actions":  st.SuccessfulActions,
		"failed_actions":      st.FailedActions,
		"last_action":         st.LastAction,
	}
}

// History returns the action history.
func (b *ArenaBridge) History() []ArenaResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]ArenaResult, len(b.hist))
	copy(out, b.hist)
	return out
}

// GetSurvivalStatus returns the current survival test status.
func (b *ArenaBridge) GetSurvivalStatus() map[string]any {
	// Arena service does not expose survival status directly.
	return map[string]any{
		"status":   "unknown",
		"progress": 0,
	}
}

// GetResilienceScore returns the resilience score from arena metrics.
func (b *ArenaBridge) GetResilienceScore() map[string]any {
	m := b.svc.Metrics()
	return map[string]any{
		"score":               b.computeResilienceScore(m),
		"avg_recovery_time":   m.AvgRecoveryTime.String(),
		"failover_count":      m.FailoverCount,
		"total_recoveries":    m.TotalRecoveries,
		"failed_recoveries":   m.FailedRecoveries,
		"data_consistency":    m.DataConsistencyRate,
	}
}

func (b *ArenaBridge) computeResilienceScore(m ares_arena.MetricsSnapshot) float64 {
	if m.TotalRecoveries == 0 {
		return 100.0
	}
	recoveryRate := float64(m.TotalRecoveries-m.FailedRecoveries) / float64(m.TotalRecoveries)
	score := recoveryRate*60 + (m.DataConsistencyRate/100)*40
	return score
}
