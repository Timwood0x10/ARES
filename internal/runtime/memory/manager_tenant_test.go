package memory

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
	memembed "github.com/Timwood0x10/ares/internal/runtime/memory/embedding"
	"github.com/Timwood0x10/ares/internal/tenantctx"
)

// tenantRecordingDistiller records the tenant each DistillConversation call
// received and returns one memory so StoreDistilledTask completes.
type tenantRecordingDistiller struct {
	mu      sync.Mutex
	tenants []string
}

func (d *tenantRecordingDistiller) DistillConversation(
	_ context.Context, _ string, _ []distillation.Message, tenantID, _ string,
) ([]distillation.Memory, error) {
	d.mu.Lock()
	d.tenants = append(d.tenants, tenantID)
	d.mu.Unlock()
	return []distillation.Memory{{
		ID:      "mem-1",
		Type:    distillation.MemoryKnowledge,
		Content: "c",
		Metadata: map[string]interface{}{
			"problem":  "p",
			"solution": "s",
		},
	}}, nil
}

// tenantRecordingExpRepo records the tenant each SearchByVector received.
type tenantRecordingExpRepo struct {
	mu          sync.Mutex
	searchedBy  []string
	created     int
	searchCalls int
}

func (r *tenantRecordingExpRepo) SearchByVector(_ context.Context, _ []float64, tenantID string, _ int) ([]distillation.Experience, error) {
	r.mu.Lock()
	r.searchedBy = append(r.searchedBy, tenantID)
	r.searchCalls++
	r.mu.Unlock()
	return nil, nil
}

func (r *tenantRecordingExpRepo) GetByMemoryType(context.Context, string, distillation.MemoryType) ([]distillation.Experience, error) {
	return nil, nil
}

func (r *tenantRecordingExpRepo) CountByMemoryType(context.Context, string, distillation.MemoryType) (int, error) {
	return 0, nil
}

func (r *tenantRecordingExpRepo) Update(context.Context, *distillation.Experience) error { return nil }
func (r *tenantRecordingExpRepo) Delete(context.Context, string) error                   { return nil }
func (r *tenantRecordingExpRepo) DeleteBatch(context.Context, []string) error            { return nil }
func (r *tenantRecordingExpRepo) Create(_ context.Context, _ *distillation.Experience) error {
	r.mu.Lock()
	r.created++
	r.mu.Unlock()
	return nil
}

// newTenantTestManager builds a memoryManager with recording fakes, mirroring
// the NewMemoryManagerWithDistiller construction but with injectable
// distiller.
func newTenantTestManager(t *testing.T) (*memoryManager, *tenantRecordingDistiller, *tenantRecordingExpRepo) {
	t.Helper()
	config := DefaultMemoryConfig()
	d := &tenantRecordingDistiller{}
	r := &tenantRecordingExpRepo{}
	pipeline, err := memembed.NewEmbeddingPipeline(&testEmbedder{})
	require.NoError(t, err)
	mgr := &memoryManager{
		sessionMemory:   memctx.NewSessionMemory(config.MaxSessions, config.SessionTTL),
		taskMemory:      memctx.NewTaskMemory(config.MaxTasks, config.TaskTTL),
		config:          config,
		distiller:       d,
		embedder:        &testEmbedder{},
		pipeline:        pipeline,
		expRepo:         r,
		ctxCleaner:      memctx.NewContextCleaner(),
		defaultTenantID: "default",
	}
	return mgr, d, r
}

// TestStoreDistilledTask_FallbackHonorsDefaultTenant is the tenant
// write/read consistency regression: the write fallback used the literal
// "default" while reads used m.defaultTenantID, so a SetDefaultTenantID
// override made every write land in a tenant the reads never looked in.
func TestStoreDistilledTask_FallbackHonorsDefaultTenant(t *testing.T) {
	mgr, d, _ := newTenantTestManager(t)
	mgr.SetDefaultTenantID("tenant-x")

	err := mgr.StoreDistilledTask(context.Background(), "task-1", &models.Task{
		Payload: map[string]any{"input": "i", "output": "o"},
	})
	require.NoError(t, err)

	d.mu.Lock()
	tenants := append([]string(nil), d.tenants...)
	d.mu.Unlock()
	require.Len(t, tenants, 1, "distiller must be called exactly once")
	require.Equal(t, "tenant-x", tenants[0],
		"write fallback must honor SetDefaultTenantID, not the literal \"default\"")
}

// TestSearchSimilarTasks_UsesDefaultTenant locks the read side of the same
// contract: searches run under the configured default tenant, snapshotted
// under RLock.
func TestSearchSimilarTasks_UsesDefaultTenant(t *testing.T) {
	mgr, _, r := newTenantTestManager(t)
	mgr.SetDefaultTenantID("tenant-x")

	_, err := mgr.SearchSimilarTasks(context.Background(), "query", 5)
	require.NoError(t, err)

	r.mu.Lock()
	searchedBy := append([]string(nil), r.searchedBy...)
	r.mu.Unlock()
	require.NotEmpty(t, searchedBy, "SearchByVector must be called")
	require.Equal(t, "tenant-x", searchedBy[0],
		"search must use the configured default tenant")
}

// TestSearchSimilarTasks_PrefersRequestTenant pins the M5 scoping fix: when the
// executing request carries a tenant, recall must use THAT tenant instead of
// the process-level default. Reading defaultTenantID unconditionally made every
// request recall whichever tenant SetDefaultTenantID last pinned — the same
// defect class the AKG recall path fixed by resolving tenantctx per request.
func TestSearchSimilarTasks_PrefersRequestTenant(t *testing.T) {
	mgr, _, r := newTenantTestManager(t)
	mgr.SetDefaultTenantID("tenant-default")

	ctx := tenantctx.With(context.Background(), "tenant-request")
	_, err := mgr.SearchSimilarTasks(ctx, "query", 5)
	require.NoError(t, err)

	r.mu.Lock()
	searchedBy := append([]string(nil), r.searchedBy...)
	r.mu.Unlock()
	require.NotEmpty(t, searchedBy, "SearchByVector must be called")
	require.Equal(t, "tenant-request", searchedBy[0],
		"recall must resolve the request's tenant, not the process-level default")

	// The configured default remains the fallback for unscoped callers.
	_, err = mgr.SearchSimilarTasks(context.Background(), "query", 5)
	require.NoError(t, err)

	r.mu.Lock()
	searchedBy = append([]string(nil), r.searchedBy...)
	r.mu.Unlock()
	require.Equal(t, "tenant-default", searchedBy[len(searchedBy)-1],
		"an unscoped request must still fall back to the configured default")
}

// TestSessionOwner_RecordsCreator pins the ownership lookup user_profile relies
// on: the user recorded at CreateSession time is read back, so a caller handed
// a session id can tell whose session it is.
func TestSessionOwner_RecordsCreator(t *testing.T) {
	mgr, _, _ := newTenantTestManager(t)

	sessionID, err := mgr.CreateSession(context.Background(), "user-1")
	require.NoError(t, err)

	owner, err := mgr.SessionOwner(context.Background(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "user-1", owner)

	// An unknown (or expired) session reports no owner rather than erroring —
	// the same "no such session" convention as GetLatestSessionForAgent.
	owner, err = mgr.SessionOwner(context.Background(), "session_nobody_1")
	require.NoError(t, err)
	require.Empty(t, owner)

	owner, err = mgr.SessionOwner(context.Background(), "")
	require.NoError(t, err)
	require.Empty(t, owner)
}
