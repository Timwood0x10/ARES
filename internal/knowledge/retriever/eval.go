package retriever

import (
	"context"

	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
)

// EvalCase is one test case for the retrieval evaluation harness (M6).
type EvalCase struct {
	// Query is the natural-language query.
	Query string `json:"query"`
	// ExpectedIDs is the set of object IDs the retrieval should return.
	// Used to compute Precision@K and Recall@K.
	ExpectedIDs []string `json:"expected_ids"`
	// Namespace scopes the evaluation to one knowledge namespace.
	Namespace string `json:"namespace,omitempty"`
}

// EvalReport is the result of running the eval harness.
type EvalReport struct {
	// Cases is the per-case results.
	Cases []EvalCaseResult `json:"cases"`
	// MeanPrecisionAtK is the average Precision@K across all cases.
	MeanPrecisionAtK float64 `json:"mean_precision_at_k"`
	// MeanRecallAtK is the average Recall@K across all cases.
	MeanRecallAtK float64 `json:"mean_recall_at_k"`
	// MeanTokensSaved is the average tokens_saved across all cases.
	MeanTokensSaved float64 `json:"mean_tokens_saved"`
}

// EvalCaseResult is the result of one eval case.
type EvalCaseResult struct {
	Query         string  `json:"query"`
	PrecisionAtK  float64 `json:"precision_at_k"`
	RecallAtK     float64 `json:"recall_at_k"`
	TokensSaved   int     `json:"tokens_saved"`
	RetrievalTier string  `json:"retrieval_tier"`
}

// TODO(tech-debt): EvalHarness is unwired — no production caller. It is also
// incomplete for what the plan (M6) asks it to prove: it measures only
// Precision@K / Recall@K / tokens_saved and does NOT run the raw-retrieval vs
// AKG comparison (nor no-result rate / wrong-relation error rate) that is the
// actual value proof. Per-case errors are swallowed and surface as
// Precision 0.
//
// EvalHarness evaluates retrieval quality (M6). It runs a set of eval cases
// through the retriever and computes Precision@K, Recall@K, and tokens_saved.
//
// The harness is deliberately stateless: it holds no data, only a retriever
// reference. Each case is run independently; results are aggregated into
// an EvalReport.
type EvalHarness struct {
	retriever *TieredRetriever
	compiler  compiler.Compiler
}

// NewEvalHarness creates an eval harness with the given tiered retriever
// and compiler. The compiler is used to measure tokens_saved per case.
func NewEvalHarness(r *TieredRetriever, c compiler.Compiler) *EvalHarness {
	return &EvalHarness{
		retriever: r,
		compiler:  c,
	}
}

// Run executes all eval cases and returns the aggregated report.
func (h *EvalHarness) Run(ctx context.Context, cases []EvalCase) EvalReport {
	if len(cases) == 0 {
		return EvalReport{}
	}

	report := EvalReport{Cases: make([]EvalCaseResult, 0, len(cases))}

	var sumPrecision, sumRecall, sumTokens float64
	for _, c := range cases {
		result := h.runCase(ctx, c)
		report.Cases = append(report.Cases, result)
		sumPrecision += result.PrecisionAtK
		sumRecall += result.RecallAtK
		sumTokens += float64(result.TokensSaved)
	}

	n := float64(len(cases))
	report.MeanPrecisionAtK = sumPrecision / n
	report.MeanRecallAtK = sumRecall / n
	report.MeanTokensSaved = sumTokens / n
	return report
}

// runCase runs one eval case and returns its result.
func (h *EvalHarness) runCase(ctx context.Context, c EvalCase) EvalCaseResult {
	query := Query{
		Text:       c.Query,
		Namespace:  c.Namespace,
		MaxResults: 50,
		MaxTokens:  4000,
	}

	result, tier, err := h.retriever.Retrieve(ctx, query)
	if err != nil {
		return EvalCaseResult{
			Query:         c.Query,
			RetrievalTier: tier.String(),
		}
	}

	// Collect retrieved IDs.
	retrievedIDs := make(map[string]bool)
	if result.Graph != nil && result.Graph.Nodes != nil {
		for id := range result.Graph.Nodes {
			retrievedIDs[id] = true
		}
	}

	// Compute Precision@K and Recall@K.
	expectedSet := make(map[string]bool, len(c.ExpectedIDs))
	for _, id := range c.ExpectedIDs {
		expectedSet[id] = true
	}

	var truePositives int
	for id := range retrievedIDs {
		if expectedSet[id] {
			truePositives++
		}
	}

	precision := 0.0
	if len(retrievedIDs) > 0 {
		precision = float64(truePositives) / float64(len(retrievedIDs))
	}
	recall := 0.0
	if len(expectedSet) > 0 {
		recall = float64(truePositives) / float64(len(expectedSet))
	}

	// Compute tokens_saved from the compiled context.
	tokensSaved := 0
	if result.Context != nil && result.Context.Metrics.TokensSaved > 0 {
		tokensSaved = result.Context.Metrics.TokensSaved
	} else if h.compiler != nil && result.Graph != nil {
		// Compile on-the-fly to get metrics.
		compiled, err := h.compiler.Compile(ctx, result.Graph, compiler.CompileConfig{
			Formats: []compiler.Format{compiler.FormatPrompt},
		})
		if err == nil && compiled != nil {
			tokensSaved = compiled.Metrics.TokensSaved
		}
	}

	return EvalCaseResult{
		Query:         c.Query,
		PrecisionAtK:  precision,
		RecallAtK:     recall,
		TokensSaved:   tokensSaved,
		RetrievalTier: tier.String(),
	}
}
