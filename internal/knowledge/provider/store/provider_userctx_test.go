package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider/store"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// TestStoreProvider_Stream_FollowsContextUser locks the read-side half of M5
// user attribution. The DistillBridge writes facts into the user's namespace
// (ResolveUserNamespace(tenant, user)), so recall must resolve the SAME
// namespace from the request-scoped user that the scheduler stamps onto the
// context (userctx). Without that, a user-scoped write lands in a namespace
// recall never looks at and the user's own facts are invisible — the exact
// read/write asymmetry that made "one user one graph" inert.
func TestStoreProvider_Stream_FollowsContextUser(t *testing.T) {
	ctx := context.Background()
	ms := memorystore.New()

	userNS := knowledge.ResolveUserNamespace("tenant-acme", "user-1", "default")
	if userNS == "tenant-acme" || userNS == "default" {
		t.Fatalf("precondition: user namespace %q must be distinct from tenant/default", userNS)
	}
	otherUserNS := knowledge.ResolveUserNamespace("tenant-acme", "user-2", "default")

	seed := []*knowledge.KnowledgeObject{
		{
			ID: "akg:user1:1", Type: knowledge.ObjectMemory, Namespace: userNS,
			Summary:    "user one decided on rust for tooling",
			Normalized: "user one rust tooling decision",
			Status:     knowledge.StatusActive, Confidence: 0.9, CreatedAt: time.Now(),
		},
		{
			ID: "akg:user2:1", Type: knowledge.ObjectMemory, Namespace: otherUserNS,
			Summary:    "user two decided on rust for tooling",
			Normalized: "user two rust tooling decision",
			Status:     knowledge.StatusActive, Confidence: 0.9, CreatedAt: time.Now(),
		},
	}
	if err := ms.Save(ctx, seed...); err != nil {
		t.Fatalf("seed save: %v", err)
	}

	p := store.New("akg_store", ms, nil, "m", "default")

	stream := func(c context.Context) []*knowledge.KnowledgeObject {
		t.Helper()
		objCh, errCh := p.Stream(c, knowledge.Intent{Goal: "rust tooling", Scope: knowledge.Scope{MaxObjects: 10}})
		var got []*knowledge.KnowledgeObject
		for obj := range objCh {
			got = append(got, obj)
		}
		if err := <-errCh; err != nil {
			t.Fatalf("stream error: %v", err)
		}
		return got
	}

	// The user scope wins over the tenant scope: recall resolves the user graph
	// and only that user's facts come back.
	got := stream(userctx.With(tenantctx.With(ctx, "tenant-acme"), "user-1"))
	if len(got) == 0 {
		t.Fatal("expected the user namespace to be searched")
	}
	for _, o := range got {
		if o.Namespace != userNS {
			t.Fatalf("recall crossed user namespaces: got %q in namespace %q, want only %q", o.ID, o.Namespace, userNS)
		}
	}

	// A different user in the same tenant recalls a different corpus.
	got = stream(userctx.With(tenantctx.With(ctx, "tenant-acme"), "user-2"))
	if len(got) == 0 {
		t.Fatal("expected the second user's namespace to be searched")
	}
	for _, o := range got {
		if o.Namespace != otherUserNS {
			t.Fatalf("recall crossed user namespaces: got %q in namespace %q, want only %q", o.ID, o.Namespace, otherUserNS)
		}
	}

	// No user: the tenant scope still applies (pre-M5 behaviour), and recall
	// must not reach into any user graph.
	got = stream(tenantctx.With(ctx, "tenant-acme"))
	for _, o := range got {
		if o.Namespace != "tenant-acme" {
			t.Fatalf("tenant-only recall reached a user graph: got %q in namespace %q", o.ID, o.Namespace)
		}
	}
}
