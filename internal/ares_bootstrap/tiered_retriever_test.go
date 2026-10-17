package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/retriever"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/Timwood0x10/ares/internal/tenantctx"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// seedTieredStore seeds the L1 (lexicon) path with two objects of differing confidence and
// returns a tiered retriever over it. L2 is intentionally nil: these tests are about the
// ADAPTER (graph → snippets, namespace, contract), and adding a semantic tier would make a
// hit ambiguous about which tier produced it.
func seedTieredStore(t *testing.T) (*retriever.TieredRetriever, *memstore.Store) {
	t.Helper()
	store := memstore.New()
	objects := []*knowledge.KnowledgeObject{
		{ID: "obj_redis", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
			Summary: "Redis for caching", Status: knowledge.StatusActive, Confidence: 0.9},
		{ID: "obj_redis_alt", Type: knowledge.ObjectMemory, Namespace: "tenant-a",
			Summary: "Redis cluster topology", Status: knowledge.StatusActive, Confidence: 0.6},
		{ID: "obj_other_tenant", Type: knowledge.ObjectMemory, Namespace: "tenant-b",
			Summary: "Redis in another tenant", Status: knowledge.StatusActive, Confidence: 0.95},
	}
	knowledge.StampWriteSchema(objects...)
	require.NoError(t, store.Save(context.Background(), objects...))
	return retriever.NewTieredRetriever(store, nil), store
}

// TestTieredContextRetriever_MapsTheGraphToSortedSnippets covers the two things that break
// silently when two shapes are adapted: the FIELD mapping (a snippet with the wrong Content
// is an empty prompt entry) and the ORDER (the manager's contract says score-descending, and
// the tiered merge does not produce that order by construction).
func TestTieredContextRetriever_MapsTheGraphToSortedSnippets(t *testing.T) {
	tiered, _ := seedTieredStore(t)
	r := newTieredContextRetriever(tiered, "tenant-a", 5)

	ctx := tenantctx.With(context.Background(), "tenant-a")
	// One keyword: L1 matches on ALL keywords (AND semantics), so a two-word query would
	// only hit the object that carries both.
	//
	// The ordering assertion is REPEATED on purpose: the snippet source is a graph NODE MAP,
	// and Go randomises map iteration, so a single call can return a sorted-looking slice by
	// luck. A test that samples once would pass against a missing sort roughly half the time
	// — which is exactly what the mutation check showed (it passed with the sort removed).
	for i := 0; i < 20; i++ {
		snippets, err := r.Retrieve(ctx, "Redis", 5)
		require.NoError(t, err)
		require.Len(t, snippets, 2, "both tenant-a objects mention Redis")
		for j := 1; j < len(snippets); j++ {
			assert.GreaterOrEqual(t, snippets[j-1].Score, snippets[j].Score,
				"snippets must be score-descending (iteration %d)", i)
		}
		if i == 0 {
			assert.Equal(t, "knowledge", snippets[0].Source)
			assert.NotEmpty(t, snippets[0].Content)
			assert.Contains(t, snippets[0].Metadata["tier"], "L1", "the tier that served is reported, not assumed")
			assert.Equal(t, "tenant-a", snippets[0].Metadata["namespace"])
		}
	}
}

// TestTieredContextRetriever_ResolvesTheNamespacePerRequest is the isolation contract, and it
// is why this adapter exists instead of a hard-coded one: the same instance must answer for
// whichever tenant the request carries.
func TestTieredContextRetriever_ResolvesTheNamespacePerRequest(t *testing.T) {
	tiered, _ := seedTieredStore(t)
	r := newTieredContextRetriever(tiered, "", 5)

	// tenant-b's object says "Redis" too; a leak would surface it under tenant-a.
	snippetsA, err := r.Retrieve(tenantctx.With(context.Background(), "tenant-a"), "Redis caching", 5)
	require.NoError(t, err)
	for _, s := range snippetsA {
		assert.Equal(t, "tenant-a", s.Metadata["namespace"], "a tenant-a request must not see tenant-b")
	}

	// The user-scoped context wins over the tenant one, matching the write side.
	userCtx := userctx.With(tenantctx.With(context.Background(), "tenant-a"), "alice")
	snippetsU, err := r.Retrieve(userCtx, "Redis caching", 5)
	require.NoError(t, err)
	for _, s := range snippetsU {
		assert.Contains(t, s.Metadata["namespace"], "alice", "a user context must scope the recall to that user")
	}
}

// TestTieredContextRetriever_HonoursTheInterfaceContract pins the two promises the interface
// makes that are easy to violate from an adapter: empty input is empty+nil, and an error
// never comes back with a slice.
func TestTieredContextRetriever_HonoursTheInterfaceContract(t *testing.T) {
	tiered, _ := seedTieredStore(t)
	r := newTieredContextRetriever(tiered, "tenant-a", 5)

	got, err := r.Retrieve(context.Background(), "", 5)
	assert.NoError(t, err)
	assert.Nil(t, got, "empty input must yield an empty slice and a nil error")

	// topK <= 0 falls back to the configured default rather than returning nothing.
	got, err = r.Retrieve(tenantctx.With(context.Background(), "tenant-a"), "Redis caching", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, got, "topK<=0 must apply the default, not skip the query")

	// No usable namespace and no fallback: L1 refuses (isolation) and there is no L2 here,
	// so the result is empty — NOT an error, because "nothing matched" is a valid answer.
	rNoScope := newTieredContextRetriever(tiered, "", 5)
	got, err = rNoScope.Retrieve(context.Background(), "Redis caching", 5)
	require.NoError(t, err)
	assert.Empty(t, got, "an unscoped request must recall nothing rather than crossing tenants")
}
