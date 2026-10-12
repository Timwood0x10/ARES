package adapter

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// recordingHybridStore captures the HybridSearch request so a test can pin the
// namespace the retriever resolved from ctx.
type recordingHybridStore struct {
	*memorystore.Store
	lastNamespace string
}

func (s *recordingHybridStore) HybridSearch(_ context.Context, req knowledge.HybridSearchRequest) ([]knowledge.ScoredObject, error) {
	s.lastNamespace = req.Namespace
	return nil, nil
}

// TestKnowledgeRetriever_HybridSearchScopedToUser pins that the AKG read loop
// resolves the request-scoped user (M5) instead of falling back to the tenant
// alone: recall must look in the same user namespace the write side distilled
// into, otherwise a user-scoped write is never read back.
func TestKnowledgeRetriever_HybridSearchScopedToUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{
			name: "user and tenant resolve the user namespace",
			ctx:  userctx.With(tenantctx.With(context.Background(), "t1"), "u1"),
			want: knowledge.UserNamespace("t1", "u1"),
		},
		{
			name: "tenant only falls back to the tenant namespace",
			ctx:  tenantctx.With(context.Background(), "t1"),
			want: "t1",
		},
		{
			// Documented single-tenant behaviour: an unscoped request keeps
			// the store's global scan, so no default namespace is invented.
			name: "unscoped stays empty for the global scan",
			ctx:  context.Background(),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingHybridStore{Store: memorystore.New()}
			r, err := NewKnowledgeRetrieverWithStore(
				context.Background(),
				buildTestRuntime(t, &ctxProvider{name: "unused", objects: []*knowledge.KnowledgeObject{}}),
				store,
				"",
				DefaultMinScore,
			)
			if err != nil {
				t.Fatalf("construct retriever: %v", err)
			}

			if _, err := r.Retrieve(tt.ctx, "redis cache", 5); err != nil {
				t.Fatalf("retrieve: %v", err)
			}
			if store.lastNamespace != tt.want {
				t.Errorf("HybridSearch namespace = %q, want %q", store.lastNamespace, tt.want)
			}
		})
	}
}
