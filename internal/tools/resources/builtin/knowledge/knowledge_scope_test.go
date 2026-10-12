package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// scopeCase is one request-context shape a knowledge tool must resolve its
// namespace from (#63): user+tenant (M5), tenant only, and unscoped.
type scopeCase struct {
	name string
	ctx  context.Context
	want string
}

func scopeCases() []scopeCase {
	return []scopeCase{
		{
			name: "user scoped context resolves the user namespace",
			ctx:  userctx.With(tenantctx.With(context.Background(), "tenant-1"), "user-1"),
			want: knowledge.UserNamespace("tenant-1", "user-1"),
		},
		{
			name: "tenant only resolves the tenant namespace",
			ctx:  tenantctx.With(context.Background(), "tenant-1"),
			want: "tenant-1",
		},
		{
			name: "unscoped falls back to the default namespace",
			ctx:  context.Background(),
			want: testDefaultNamespace,
		},
	}
}

// TestKnowledgeSearch_Execute_ScopesFromContext pins that search resolves its
// namespace from the request context — the user's own graph under M5 — and
// never from a tool parameter: the LLM-supplied tenant_id must not reach the
// searcher.
func TestKnowledgeSearch_Execute_ScopesFromContext(t *testing.T) {
	for _, tc := range scopeCases() {
		t.Run(tc.name, func(t *testing.T) {
			s := &mockSearcher{}
			s.On("Search", mock.Anything, tc.want, "test").Return([]*RetrievalResult{}, nil)
			ks := NewKnowledgeSearch(s)

			result, err := ks.Execute(tc.ctx, map[string]interface{}{
				"query": "test",
				// Ignored: must never widen the scope.
				"tenant_id": "attacker-tenant",
			})
			assert.NoError(t, err)
			assert.True(t, result.Success)
			s.AssertExpectations(t)
		})
	}
}

// TestKnowledgeAdd_Execute_ScopesFromContext pins the write side: an added item
// is created under the context-resolved scope.
func TestKnowledgeAdd_Execute_ScopesFromContext(t *testing.T) {
	for _, tc := range scopeCases() {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockKnowledgeService{}
			svc.On("AddKnowledge", mock.Anything, mock.MatchedBy(func(item *KnowledgeItem) bool {
				return item.Namespace == tc.want && item.Content == "test"
			})).Return(&KnowledgeItem{ID: "1", Namespace: tc.want, Content: "test"}, nil)
			ka := NewKnowledgeAdd(svc)

			result, err := ka.Execute(tc.ctx, map[string]interface{}{
				"content":   "test",
				"tenant_id": "attacker-tenant",
			})
			assert.NoError(t, err)
			assert.True(t, result.Success)
			svc.AssertExpectations(t)
		})
	}
}

// TestKnowledgeUpdate_Execute_ScopesFromContext pins the update side, so a
// caller cannot update outside the scope the request actually owns.
func TestKnowledgeUpdate_Execute_ScopesFromContext(t *testing.T) {
	for _, tc := range scopeCases() {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockKnowledgeService{}
			svc.On("GetKnowledge", mock.Anything, tc.want, "1").Return(nil, nil)
			ku := NewKnowledgeUpdate(svc)

			result, err := ku.Execute(tc.ctx, map[string]interface{}{
				"item_id": "1",
				"content": "new content",
			})
			assert.NoError(t, err)
			// Not found under the resolved scope; the mock pins which scope was
			// looked up, so a wrong scope fails AssertExpectations.
			assert.False(t, result.Success)
			svc.AssertExpectations(t)
		})
	}
}

// TestKnowledgeDelete_Execute_ScopesFromContext pins the delete side to the
// same rule, so a user cannot delete outside their own graph.
func TestKnowledgeDelete_Execute_ScopesFromContext(t *testing.T) {
	for _, tc := range scopeCases() {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mockKnowledgeService{}
			svc.On("DeleteKnowledge", mock.Anything, tc.want, "1").Return(nil)
			kd := NewKnowledgeDelete(svc)

			result, err := kd.Execute(tc.ctx, map[string]interface{}{
				"item_id":   "1",
				"tenant_id": "attacker-tenant",
			})
			assert.NoError(t, err)
			assert.True(t, result.Success)
			svc.AssertExpectations(t)
		})
	}
}
