package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// TestKnowledgeNamespace_Precedence pins that the distill_memory write side
// resolves the namespace with the same precedence as every other AKG path
// (AKG distill bridge write, StoreProvider read): user > tenant > default.
func TestKnowledgeNamespace_Precedence(t *testing.T) {
	t.Parallel()

	both := userctx.With(tenantctx.With(context.Background(), "t1"), "u1")
	if got, want := knowledgeNamespace(both), knowledge.UserNamespace("t1", "u1"); got != want {
		t.Errorf("user+tenant should resolve to the user namespace: got %q want %q", got, want)
	}

	tenantOnly := tenantctx.With(context.Background(), "t1")
	if got := knowledgeNamespace(tenantOnly); got != "t1" {
		t.Errorf("tenant-only should resolve to the tenant namespace: got %q", got)
	}

	if got := knowledgeNamespace(context.Background()); got != defaultKnowledgeNamespace {
		t.Errorf("unscoped should fall back to %q: got %q", defaultKnowledgeNamespace, got)
	}
}

// TestDistillMemory_UserScoped_ReadBackBySameUser is the regression test for
// the write/read asymmetry this fix closed: before M5 the tool stamped the
// tenant namespace only, so a memory it persisted was invisible to the
// user-scoped recall that later looked in the user namespace — written but
// never read back.
func TestDistillMemory_UserScoped_ReadBackBySameUser(t *testing.T) {
	t.Parallel()

	store := memorystore.New()
	svc := NewAKFServiceWithStore(nil, &testCompiler{}, store, knowledge.DefaultQualityGateConfig())

	ctx := userctx.With(tenantctx.With(context.Background(), "tenant-1"), "user-1")
	input, err := json.Marshal(map[string]any{
		"content": "the user prefers table-driven Go tests",
		"type":    "memory",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if _, err := svc.handleDistillMemory(ctx, string(input)); err != nil {
		t.Fatalf("distill_memory failed: %v", err)
	}

	// The read side (StoreProvider.namespaceFor / context retriever) resolves
	// this very namespace from the same ctx.
	userNS := knowledge.ResolveUserNamespace(tenantctx.From(ctx), userctx.From(ctx), "")
	if userNS == "" || userNS == "tenant-1" {
		t.Fatalf("expected a user-scoped namespace, got %q", userNS)
	}

	got, err := store.Query(ctx, knowledge.Query{Namespace: userNS, Limit: 10})
	if err != nil {
		t.Fatalf("query user namespace: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("memory written under a user ctx is not readable back from its own namespace %q", userNS)
	}

	// Isolate: another user in the same tenant must not see it.
	other, err := store.Query(ctx, knowledge.Query{Namespace: knowledge.UserNamespace("tenant-1", "user-2"), Limit: 10})
	if err != nil {
		t.Fatalf("query other user namespace: %v", err)
	}
	if len(other) != 0 {
		t.Errorf("cross-user leak: user-2 saw %d object(s) written by user-1", len(other))
	}

	// And it must NOT live in the tenant namespace — that is exactly the
	// pre-M5 placement that made the object unreachable from user recall.
	tenantOnly, err := store.Query(ctx, knowledge.Query{Namespace: "tenant-1", Limit: 10})
	if err != nil {
		t.Fatalf("query tenant namespace: %v", err)
	}
	if len(tenantOnly) != 0 {
		t.Errorf("user-scoped memory leaked into the tenant namespace: %d object(s)", len(tenantOnly))
	}
}
