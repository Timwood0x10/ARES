package retrieval

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Error variable tests
// ---------------------------------------------------------------------------

func TestErrorVariables(t *testing.T) {
	assert.Equal(t, "invalid tenant ID", ErrInvalidTenantID.Error())
	assert.Equal(t, "invalid query", ErrInvalidQuery.Error())
	assert.Equal(t, "no retrieval service configured", ErrNoRetrievalService.Error())
	assert.Equal(t, "search failed", ErrSearchFailed.Error())
}

// ---------------------------------------------------------------------------
// NewService tests
// ---------------------------------------------------------------------------

func TestNewService_NilConfig_UsesDefaults(t *testing.T) {
	svc, err := NewService(nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
	// nil config defaults: UseSimpleRetrieval=true -> simpleRetrieval is set
	assert.NotNil(t, svc.simpleRetrieval)
	assert.Nil(t, svc.advancedRetrieval)
	assert.Nil(t, svc.pool)
}

func TestNewService_SimpleRetrievalDisabled(t *testing.T) {
	cfg := &Config{UseSimpleRetrieval: false}
	svc, err := NewService(nil, nil, nil, cfg)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.Nil(t, svc.simpleRetrieval)
	assert.Nil(t, svc.advancedRetrieval)
}

func TestNewService_SimpleRetrievalEnabled(t *testing.T) {
	cfg := &Config{UseSimpleRetrieval: true, TopK: 20, MinScore: 0.5}
	svc, err := NewService(nil, nil, nil, cfg)
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.NotNil(t, svc.simpleRetrieval)
	assert.Nil(t, svc.advancedRetrieval)
}

// ---------------------------------------------------------------------------
// Search validation tests
// ---------------------------------------------------------------------------

func TestSearch_EmptyTenantID(t *testing.T) {
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: true})
	require.NoError(t, err)

	_, err = svc.Search(context.Background(), "", "query")
	assert.ErrorIs(t, err, ErrInvalidTenantID)
}

func TestSearch_EmptyQuery(t *testing.T) {
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: true})
	require.NoError(t, err)

	_, err = svc.Search(context.Background(), "tenant-1", "")
	assert.ErrorIs(t, err, ErrInvalidQuery)
}

func TestSearch_NoRetrievalService(t *testing.T) {
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: false})
	require.NoError(t, err)
	require.Nil(t, svc.simpleRetrieval)

	_, err = svc.Search(context.Background(), "tenant-1", "query")
	assert.ErrorIs(t, err, ErrNoRetrievalService)
}

// ---------------------------------------------------------------------------
// SearchWithConfig validation tests
// ---------------------------------------------------------------------------

func TestSearchWithConfig_EmptyTenantID(t *testing.T) {
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: true})
	require.NoError(t, err)

	_, err = svc.SearchWithConfig(context.Background(), "", "query", &Config{})
	assert.ErrorIs(t, err, ErrInvalidTenantID)
}

func TestSearchWithConfig_EmptyQuery(t *testing.T) {
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: true})
	require.NoError(t, err)

	_, err = svc.SearchWithConfig(context.Background(), "tenant-1", "", &Config{})
	assert.ErrorIs(t, err, ErrInvalidQuery)
}

func TestSearchWithConfig_NilConfig_FallsBackToSearch(t *testing.T) {
	// With UseSimpleRetrieval=false and no simpleRetrieval,
	// Search will fail with ErrNoRetrievalService.
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: false})
	require.NoError(t, err)

	_, err = svc.SearchWithConfig(context.Background(), "tenant-1", "query", nil)
	assert.ErrorIs(t, err, ErrNoRetrievalService)
}

func TestSearchWithConfig_NoRetrievalService(t *testing.T) {
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: false})
	require.NoError(t, err)

	_, err = svc.SearchWithConfig(context.Background(), "tenant-1", "query", &Config{})
	assert.ErrorIs(t, err, ErrNoRetrievalService)
}

func TestSearchWithConfig_AdvancedMode(t *testing.T) {
	// With UseSimpleRetrieval=false, SearchWithConfig should return
	// ErrNoRetrievalService regardless of the config provided.
	svc, err := NewService(nil, nil, nil, &Config{UseSimpleRetrieval: false})
	require.NoError(t, err)

	cfg := &Config{TopK: 5, MinScore: 0.8}
	_, err = svc.SearchWithConfig(context.Background(), "tenant-1", "query", cfg)
	assert.ErrorIs(t, err, ErrNoRetrievalService)
}

// ---------------------------------------------------------------------------
// Result type
// ---------------------------------------------------------------------------

func TestResultStruct(t *testing.T) {
	r := &Result{
		Content:   "content",
		Source:    "source",
		Score:     0.95,
		SubSource: "simple",
	}
	assert.Equal(t, "content", r.Content)
	assert.Equal(t, "source", r.Source)
	assert.Equal(t, 0.95, r.Score)
	assert.Equal(t, "simple", r.SubSource)
}

// ---------------------------------------------------------------------------
// Config defaults
// ---------------------------------------------------------------------------

func TestNewService_NilConfigFields(t *testing.T) {
	// Verify nil config produces default field values in the config struct
	svc, err := NewService(nil, nil, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
	// The simpleRetrieval was created with the default config values
	// We can't easily inspect the internals of simpleRetrieval,
	// but we know it was created with TopK=10 and MinScore=0.4
	assert.NotNil(t, svc.simpleRetrieval)
}
