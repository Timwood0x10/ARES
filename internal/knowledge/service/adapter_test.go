package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
	apiknowledge "github.com/Timwood0x10/ares/internal/knowledgeapi"
)

// TestNewServiceAdapter_NilRuntimeReturnsError verifies the nil guard.
func TestNewServiceAdapter_NilRuntimeReturnsError(t *testing.T) {
	_, err := NewServiceAdapter(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

// TestBuildGraph_EmptyGoalReturnsErrNilIntent verifies the input validation.
func TestBuildGraph_EmptyGoalReturnsErrNilIntent(t *testing.T) {
	adapter, err := NewServiceAdapter(runtime.New(nil, nil, nil, nil, nil, nil))
	require.NoError(t, err)

	_, err = adapter.BuildGraph(context.Background(), apiknowledge.Intent{})
	require.Error(t, err)
	assert.ErrorIs(t, err, apiknowledge.ErrNilIntent)
}

// TestCompileContext_NilGraphReturnsErrNilGraph verifies the nil graph guard.
func TestCompileContext_NilGraphReturnsErrNilGraph(t *testing.T) {
	adapter, err := NewServiceAdapter(runtime.New(nil, nil, nil, nil, nil, nil))
	require.NoError(t, err)

	_, err = adapter.CompileContext(context.Background(), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, apiknowledge.ErrNilGraph)
}

// TestDistill_EmptyTenantIDReturnsErr verifies the tenant guard.
func TestDistill_EmptyTenantIDReturnsErr(t *testing.T) {
	adapter, err := NewServiceAdapter(runtime.New(nil, nil, nil, nil, nil, nil))
	require.NoError(t, err)

	_, err = adapter.Distill(context.Background(), []byte("data"), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, apiknowledge.ErrEmptyTenantID)
}

// TestDistill_EmptyMemoryReturnsNil verifies the empty-input guard.
func TestDistill_EmptyMemoryReturnsNil(t *testing.T) {
	adapter, err := NewServiceAdapter(runtime.New(nil, nil, nil, nil, nil, nil))
	require.NoError(t, err)

	objs, err := adapter.Distill(context.Background(), []byte{}, "tenant-1")
	require.NoError(t, err)
	assert.Empty(t, objs)
}

// TestCompileContext_NonNilGraphProducesMarkdown verifies the happy path
// produces a non-empty markdown string.
func TestCompileContext_NonNilGraphProducesMarkdown(t *testing.T) {
	adapter, err := NewServiceAdapter(runtime.New(nil, nil, nil, nil, nil, nil))
	require.NoError(t, err)

	graph := &apiknowledge.WorkingGraph{
		Nodes: map[string]*apiknowledge.KnowledgeObject{
			"node-1": {
				ID:      "node-1",
				Type:    apiknowledge.ObjectMemory,
				Summary: "test summary",
			},
		},
	}
	out, err := adapter.CompileContext(context.Background(), graph)
	require.NoError(t, err)
	assert.Contains(t, out, "node-1")
	assert.Contains(t, out, "test summary")
}

// TestQuery_ReturnsEmpty verifies the stateless query path returns
// an empty slice (not nil, not an error).
func TestQuery_ReturnsEmpty(t *testing.T) {
	adapter, err := NewServiceAdapter(runtime.New(nil, nil, nil, nil, nil, nil))
	require.NoError(t, err)

	objs, err := adapter.Query(context.Background(), apiknowledge.Query{Limit: 10})
	require.NoError(t, err)
	assert.Empty(t, objs)
}

// TestServiceAdapter_DistillStableContentID locks REVIEW 2.5#30: the
// distilled object ID must be derived from the CONTENT (hash), not the byte
// length. Two different memories of equal length previously collided on the
// same "distilled-<len>" ID, silently overwriting each other wherever IDs
// are primary keys.
func TestServiceAdapter_DistillStableContentID(t *testing.T) {
	adapter, err := NewServiceAdapter(runtime.New(nil, nil, nil, nil, nil, nil))
	require.NoError(t, err)
	ctx := context.Background()

	// Same length, different content → different IDs.
	a, err := adapter.Distill(ctx, []byte("payment failed twice"), "tenant-a")
	require.NoError(t, err)
	b, err := adapter.Distill(ctx, []byte("network timeout!"), "tenant-a")
	require.NoError(t, err)
	require.Len(t, a, 1)
	require.Len(t, b, 1)
	assert.NotEqual(t, a[0].ID, b[0].ID, "equal-length different-content memories collided")

	// Same content → same ID (idempotent re-distill).
	c, err := adapter.Distill(ctx, []byte("payment failed twice"), "tenant-a")
	require.NoError(t, err)
	assert.Equal(t, a[0].ID, c[0].ID, "same content must produce a stable ID")

	// Same content, different tenant → different IDs: the store's ID space
	// is global, so tenant-scoped hashing keeps tenants from sharing (and
	// overwriting) one object.
	d, err := adapter.Distill(ctx, []byte("payment failed twice"), "tenant-b")
	require.NoError(t, err)
	assert.NotEqual(t, a[0].ID, d[0].ID, "identical content in different tenants must not share an ID")
	assert.Equal(t, "tenant-b", d[0].Namespace)

	// ID shape: stable prefix, hex body.
	if !strings.HasPrefix(a[0].ID, "distilled-") {
		t.Errorf("ID must keep the distilled- prefix, got %q", a[0].ID)
	}
	if body := strings.TrimPrefix(a[0].ID, "distilled-"); len(body) != 32 {
		t.Errorf("expected 32 hex chars (16 bytes), got %d in %q", len(body), body)
	}
}
