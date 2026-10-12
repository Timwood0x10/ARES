package retriever_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/retriever"
	memstore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// rawBody builds a large source body: the material a consumer would feed the
// model if it skipped AKG and used the stored records directly.
func rawBody(s string) []byte {
	return []byte(strings.Repeat(s+". ", 120))
}

// TestAKG_EvalHarness_CorrectnessVsRaw is the M6 value proof: the harness must
// report the AKG result AGAINST the raw baseline — the whole namespace,
// unranked, which is what a consumer feeds the model when it skips AKG — rather
// than in isolation. Without the comparison "tokens -60% but correctness -20%"
// is invisible, and a saving bought by dropping relevant facts reads as a win.
func TestAKG_EvalHarness_CorrectnessVsRaw(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memstore.New()

	// Summaries stay short and lexically matchable; Raw carries the large source
	// body, so the raw baseline is expensive and the compiled context is not.
	objects := []*knowledge.KnowledgeObject{
		{
			ID: "obj_redis", Type: knowledge.ObjectMemory, Namespace: "ns",
			Summary: "Redis for caching", Normalized: "Redis for caching",
			Raw:    rawBody("the original conversation about redis caching"),
			Status: knowledge.StatusActive,
		},
		{
			ID: "obj_postgres", Type: knowledge.ObjectMemory, Namespace: "ns",
			Summary: "PostgreSQL for persistence", Normalized: "PostgreSQL for persistence",
			Raw:    rawBody("the original conversation about postgres persistence"),
			Status: knowledge.StatusActive,
		},
	}
	knowledge.StampWriteSchema(objects...)
	require.NoError(t, store.Save(ctx, objects...))

	tr := retriever.NewTieredRetriever(store, nil)
	harness := retriever.NewEvalHarness(tr, compiler.NewDefaultCompiler())

	report := harness.Run(ctx, []retriever.EvalCase{
		{Query: "Redis caching", Namespace: "ns", ExpectedIDs: []string{"obj_redis"}},
		{Query: "PostgreSQL persistence", Namespace: "ns", ExpectedIDs: []string{"obj_postgres"}},
	})

	// The raw baseline is the whole namespace, so its recall is the ceiling the
	// AKG result must not fall below: giving up a relevant fact to save tokens
	// is exactly the failure this number exists to catch.
	require.Equal(t, 1.0, report.MeanRawRecallAtK,
		"the raw baseline must recall every expected fact")
	require.Equal(t, 1.0, report.MeanRecallAtK,
		"AKG must not drop a relevant fact the raw baseline holds")
	require.Zero(t, report.RecallDelta,
		"correctness must be preserved, not traded away for tokens")

	// ...while the tiered selection is more precise than feeding everything.
	require.Less(t, report.MeanRawPrecisionAtK, report.MeanPrecisionAtK,
		"selecting beats dumping the whole corpus")

	// The saving is measured end to end, not only within the retrieved subset.
	require.Greater(t, report.MeanCorpusTokens, 0.0, "corpus tokens must be measured")
	require.Greater(t, report.MeanContextTokens, 0.0, "context tokens must be measured")
	require.Greater(t, report.EndToEndTokensSaved, 0.0,
		"the compiled context must cost less than the raw corpus it replaces")

	// No failure may hide behind Precision 0.
	require.Zero(t, report.ErrorRate)
	require.Zero(t, report.NoResultRate)
	for _, c := range report.Cases {
		require.Empty(t, c.Err, "case %q failed and must say so", c.Query)
		require.Greater(t, c.RetrievedCount, 0, "case %q retrieved nothing", c.Query)
	}
}

// TestAKG_EvalHarness_ReportsCorrectnessLoss is the negative half of the value
// proof: when the tiered selection loses a fact the raw corpus still holds, the
// harness must report it as a negative RecallDelta rather than leaving the
// token saving to read as a win.
func TestAKG_EvalHarness_ReportsCorrectnessLoss(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := memstore.New()
	objects := []*knowledge.KnowledgeObject{
		{
			ID: "obj_kafka", Type: knowledge.ObjectMemory, Namespace: "ns",
			Summary: "Kafka for event streaming", Normalized: "Kafka for event streaming",
			Raw:    rawBody("the original conversation about kafka"),
			Status: knowledge.StatusActive,
		},
	}
	knowledge.StampWriteSchema(objects...)
	require.NoError(t, store.Save(ctx, objects...))

	tr := retriever.NewTieredRetriever(store, nil)
	harness := retriever.NewEvalHarness(tr, compiler.NewDefaultCompiler())

	// "message queue" shares no token with the object's Summary, so the lexicon
	// tier cannot reach it: the fact is in the corpus but the selection misses it.
	report := harness.Run(ctx, []retriever.EvalCase{
		{Query: "message queue", Namespace: "ns", ExpectedIDs: []string{"obj_kafka"}},
	})

	require.Equal(t, 1.0, report.MeanRawRecallAtK, "the raw corpus still holds the fact")
	require.Zero(t, report.MeanRecallAtK, "the tiered selection misses it")
	require.Equal(t, -1.0, report.RecallDelta,
		"the harness must report the correctness loss, not hide it behind the token saving")
	require.Equal(t, 1.0, report.NoResultRate)
}

// TestAKG_EvalHarness_SurfacesNoResult pins that an empty result set is COUNTED
// rather than swallowed: an unscoped case cannot be searched (L1 refuses to
// recall across namespaces), which is a no-result and must show up as one.
func TestAKG_EvalHarness_SurfacesNoResult(t *testing.T) {
	t.Parallel()

	tr := retriever.NewTieredRetriever(memstore.New(), nil)
	harness := retriever.NewEvalHarness(tr, nil)

	report := harness.Run(context.Background(), []retriever.EvalCase{
		{Query: "anything", ExpectedIDs: []string{"obj_1"}},
	})

	require.Equal(t, 1.0, report.NoResultRate,
		"an unscoped case retrieves nothing and must be counted as such")
	require.Zero(t, report.ErrorRate, "a refusal to search unscoped is not an error")
	require.Len(t, report.Cases, 1)
	require.Zero(t, report.Cases[0].RetrievedCount)
	require.Empty(t, report.Cases[0].Err)
}
