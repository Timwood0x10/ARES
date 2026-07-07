package ares_runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 5*time.Second, cfg.HeartbeatInterval)
	assert.Equal(t, 30*time.Second, cfg.HeartbeatTimeout)
	assert.Equal(t, 3, cfg.MaxMissedHeartbeats)
	assert.Equal(t, 10, cfg.MaxRestartsPerAgent)
	assert.Equal(t, 60*time.Second, cfg.ResurrectTimeout)
	assert.True(t, cfg.UseMemoryStore)
}

func TestNewService_DefaultConfig(t *testing.T) {
	svc, err := NewService(Config{UseMemoryStore: true}, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.NotNil(t, svc.EventStore())
}

func TestNewService_CustomConfig(t *testing.T) {
	cfg := Config{
		HeartbeatInterval:   10 * time.Second,
		HeartbeatTimeout:    60 * time.Second,
		MaxMissedHeartbeats: 5,
		MaxRestartsPerAgent: 3,
		ResurrectTimeout:    30 * time.Second,
		UseMemoryStore:      true,
	}
	svc, err := NewService(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

func TestNewService_RequiresExternalStore(t *testing.T) {
	cfg := Config{
		UseMemoryStore: false,
	}
	svc, err := NewService(cfg, nil)
	require.Error(t, err)
	require.Nil(t, svc)
	assert.Contains(t, err.Error(), "event store required")
}

func TestNewService_ZeroValuesGetDefaults(t *testing.T) {
	// All zero values should be replaced by defaults internally.
	svc, err := NewService(Config{UseMemoryStore: true}, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)

	// Verify we can get stats from the underlying runtime.
	stats := svc.Stats()
	require.NotNil(t, stats)
}

func TestService_GetAgent_NotFound(t *testing.T) {
	svc, err := NewService(Config{UseMemoryStore: true}, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)

	agent := svc.GetAgent("non-existent")
	assert.Nil(t, agent)
}

func TestService_EventStore(t *testing.T) {
	svc, err := NewService(Config{UseMemoryStore: true}, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)

	es := svc.EventStore()
	assert.NotNil(t, es)
}
