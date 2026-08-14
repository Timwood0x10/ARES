package skills

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_RegisterAndListProgressive(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(Skill{Name: "shell", Description: "Run shell commands", Detail: "Long body..."}))
	require.NoError(t, r.Register(Skill{Name: "web-search", Description: "Search the web", Detail: "Another long body..."}))

	listed := r.List()
	require.Len(t, listed, 2)
	// Progressive disclosure: Detail must NOT be in the resident list.
	for _, s := range listed {
		assert.Empty(t, s.Detail, "List must omit Detail (progressive disclosure)")
		assert.NotEmpty(t, s.Description, "Description is always resident")
	}
	// Deterministic order.
	assert.Equal(t, "shell", listed[0].Name)
	assert.Equal(t, "web-search", listed[1].Name)
}

func TestRegistry_LoadDetailOnDemand(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(Skill{Name: "shell", Description: "Run shell commands", Detail: "Full body"}))

	detail, ok := r.LoadDetail("shell")
	require.True(t, ok)
	assert.Equal(t, "Full body", detail)

	_, ok = r.LoadDetail("missing")
	assert.False(t, ok, "unknown skill must report not-found")
}

func TestRegistry_HasAndCount(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(Skill{Name: "a", Description: "d"}))
	require.NoError(t, r.Register(Skill{Name: "b", Description: "d"}))

	assert.True(t, r.Has("a"))
	assert.False(t, r.Has("zzz"))
	assert.Equal(t, 2, r.Count())
}

func TestRegistry_RegisterEmptyNameRejected(t *testing.T) {
	r := NewRegistry()
	err := r.Register(Skill{Name: "", Description: "d"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "name must not be empty")
}
