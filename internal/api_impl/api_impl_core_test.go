package apiimpl

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCoreMultiMCPAdapter_NewAndList(t *testing.T) {
	adapter := NewMultiMCPAdapter(nil)
	tools, err := adapter.ListTools(context.Background())
	assert.NoError(t, err)
	assert.Empty(t, tools)
}

func TestCoreMultiMCPAdapter_CallToolNotFound(t *testing.T) {
	adapter := NewMultiMCPAdapter(nil)
	_, err := adapter.CallTool(context.Background(), "nonexistent", nil)
	assert.Error(t, err)
}

func TestCoreArenaAdapter_StatsEmpty(t *testing.T) {
	adapter := &ArenaAdapter{}
	stats := adapter.Stats()
	assert.NotNil(t, stats)
}

func TestCoreArenaAdapter_HistoryEmpty(t *testing.T) {
	adapter := &ArenaAdapter{}
	history := adapter.History()
	assert.Empty(t, history)
}

func TestCoreArenaAdapter_GetResilienceScore(t *testing.T) {
	adapter := &ArenaAdapter{}
	score := adapter.GetResilienceScore()
	assert.NotNil(t, score)
}
