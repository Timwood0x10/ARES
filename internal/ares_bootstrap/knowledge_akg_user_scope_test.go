package ares_bootstrap

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	storeprovider "github.com/Timwood0x10/ares/internal/knowledge/provider/store"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// recordingConversationDistiller records the scope each distillation received
// and returns one memory per conversation, keyed by its id, so two "sessions"
// produce two distinguishable facts.
type recordingConversationDistiller struct {
	mu       sync.Mutex
	tenants  []string
	users    []string
	contents map[string]string
}

func (d *recordingConversationDistiller) DistillConversation(
	_ context.Context, conversationID string, _ []distillation.Message, tenantID, userID string,
) ([]distillation.Memory, error) {
	d.mu.Lock()
	d.tenants = append(d.tenants, tenantID)
	d.users = append(d.users, userID)
	content := d.contents[conversationID]
	d.mu.Unlock()
	return []distillation.Memory{{
		ID:      "mem-" + conversationID,
		Type:    distillation.MemoryKnowledge,
		Content: content,
	}}, nil
}

// TestAKG_OneUserOneGraph_SessionsAccumulate is the M5 end-to-end acceptance:
// the AKG write path takes the user from the task event payload and resolves
// the SAME user-level namespace the recall path resolves, so two separate
// sessions of one user accumulate into ONE graph — while a second user in the
// same tenant stays isolated from it.
//
// This is the half the per-layer tests cannot show. They pin the namespace
// encoding (knowledge.UserNamespace), the write side (adapter.DistillBridge)
// and the read side (provider/store) separately, but none of them proves that a
// fact written from an event is recallable by the same user and invisible to
// another — which is the whole promise of "the longer you use it, the better it
// knows you".
func TestAKG_OneUserOneGraph_SessionsAccumulate(t *testing.T) {
	const (
		tenantID = "tenant-acme"
		userID   = "user-1"
		otherID  = "user-2"
	)

	store := memorystore.New()
	distiller := &recordingConversationDistiller{contents: map[string]string{
		"conv-1": "the user prefers rust tooling",
		"conv-2": "the user prefers rust tooling for reviews",
	}}
	// MinFinalScore 0 keeps promotion deterministic: scoreQuality always marks
	// objects as candidates, so a zero threshold promotes every fact to active
	// and the recall path (StatusFilter=[StatusActive]) sees it. No embedding
	// service and no dedup, so the two sessions stay distinct facts.
	bridge := adapter.NewDistillBridgeWithGate(
		distiller, nil, store, nil,
		knowledge.QualityGateConfig{MinFinalScore: 0, MaxFactsPerIngest: 50},
		nil, "default", "",
	)

	// Two sessions of the SAME user, delivered the way production delivers
	// them: as task-completion events whose payload carries the user.
	var eg errgroup.Group
	for _, conv := range []string{"conv-1", "conv-2"} {
		triggerAKGBridge(context.Background(), &eg, &ares_events.Event{
			ID:       conv,
			StreamID: "stream-" + conv,
			Type:     ares_events.EventTaskCompleted,
			Payload: map[string]any{
				ares_events.EventKeyTask:     "please remember my rust tooling preferences (" + conv + ")",
				ares_events.EventKeyResult:   "noted, I will remember your rust tooling preferences from now on",
				ares_events.EventKeyTenantID: tenantID,
				ares_events.EventKeyUserID:   userID,
			},
		}, bridge)
	}
	require.NoError(t, eg.Wait())

	distiller.mu.Lock()
	users := append([]string(nil), distiller.users...)
	tenants := append([]string(nil), distiller.tenants...)
	distiller.mu.Unlock()
	require.Equal(t, []string{userID, userID}, users,
		"both sessions must distil under the user carried by the event payload")
	require.Equal(t, []string{tenantID, tenantID}, tenants)

	// Recall through the production read path (StoreProvider.namespaceFor),
	// never by querying a namespace the test computed itself.
	recall := func(ctx context.Context) []*knowledge.KnowledgeObject {
		t.Helper()
		p := storeprovider.New("akg_store", store, nil, "", "default")
		objCh, errCh := p.Stream(ctx, knowledge.Intent{
			Goal:  "rust tooling",
			Scope: knowledge.Scope{MaxObjects: 10},
		})
		var got []*knowledge.KnowledgeObject
		for obj := range objCh {
			got = append(got, obj)
		}
		require.NoError(t, <-errCh)
		return got
	}

	// One user, one graph: both sessions' facts come back for the same user.
	mine := recall(userctx.With(tenantctx.With(context.Background(), tenantID), userID))
	require.Len(t, mine, 2,
		"both sessions must accumulate into the one user graph, got %d object(s)", len(mine))

	// Isolation is per USER, not per tenant: another user in the same tenant
	// recalls nothing.
	theirs := recall(userctx.With(tenantctx.With(context.Background(), tenantID), otherID))
	require.Empty(t, theirs, "user-2 must not recall user-1's graph")
}
