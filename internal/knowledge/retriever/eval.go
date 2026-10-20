package retriever

import (
	"context"

	"github.com/Timwood0x10/ares/internal/knowledge"
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

	// MeanRawPrecisionAtK and MeanRawRecallAtK are the same metrics for the RAW
	// baseline — the whole namespace, unranked, which is what a consumer feeds
	// the model when it skips AKG. RawRecallAtK is 1.0 whenever the corpus holds
	// the expected objects, so it is the reference the AKG numbers are read
	// against.
	MeanRawPrecisionAtK float64 `json:"mean_raw_precision_at_k"`
	MeanRawRecallAtK    float64 `json:"mean_raw_recall_at_k"`
	// RecallDelta is MeanRecallAtK - MeanRawRecallAtK: the correctness the
	// tiering gave up. The plan's value test — "tokens -60% but correctness
	// -20% is not success" — turns on this number. Unclamped, so a regression
	// stays visible instead of being rounded away at zero.
	RecallDelta float64 `json:"recall_delta"`

	// MeanCorpusTokens is the raw token cost of the whole namespace per case;
	// MeanContextTokens is the compiled AKG context per case.
	// EndToEndTokensSaved is their difference — the saving relative to feeding
	// raw records, as opposed to MeanTokensSaved which is the compression WITHIN
	// the retrieved subset only.
	MeanCorpusTokens    float64 `json:"mean_corpus_tokens"`
	MeanContextTokens   float64 `json:"mean_context_tokens"`
	EndToEndTokensSaved float64 `json:"end_to_end_tokens_saved"`

	// NoResultRate is the share of cases that retrieved nothing, and ErrorRate
	// the share that failed outright. Both were previously invisible: a failed
	// case surfaced only as Precision 0, indistinguishable from a genuine miss.
	NoResultRate float64 `json:"no_result_rate"`
	ErrorRate    float64 `json:"error_rate"`
}

// EvalCaseResult is the result of one eval case.
type EvalCaseResult struct {
	Query         string  `json:"query"`
	PrecisionAtK  float64 `json:"precision_at_k"`
	RecallAtK     float64 `json:"recall_at_k"`
	TokensSaved   int     `json:"tokens_saved"`
	RetrievalTier string  `json:"retrieval_tier"`

	// RawPrecisionAtK and RawRecallAtK are the RAW-baseline counterparts of
	// Precision/Recall: the whole namespace, unranked.
	RawPrecisionAtK float64 `json:"raw_precision_at_k"`
	RawRecallAtK    float64 `json:"raw_recall_at_k"`

	// CorpusTokens is the raw token cost of the whole namespace for this case;
	// ContextTokens is the compiled AKG context. Both are 0 when no compiler is
	// wired.
	CorpusTokens  int `json:"corpus_tokens"`
	ContextTokens int `json:"context_tokens"`

	// RetrievedCount is how many objects AKG returned for this case.
	RetrievedCount int `json:"retrieved_count"`

	// Err surfaces a per-case failure instead of letting it masquerade as a
	// miss with Precision 0.
	Err string `json:"error,omitempty"`
}

// TODO(tech-debt): EvalHarness is unwired — no production caller. The raw
// (whole-corpus, unranked) vs AKG comparison, the no-result rate and per-case
// error visibility are now measured; still missing from what the plan (M6) asks
// for as the value proof are the wrong-relation error rate and the
// evidence-coverage rate.
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
	var sumRawPrecision, sumRawRecall, sumCorpus, sumContext float64
	var noResult, errored int
	for _, c := range cases {
		result := h.runCase(ctx, c)
		report.Cases = append(report.Cases, result)
		sumPrecision += result.PrecisionAtK
		sumRecall += result.RecallAtK
		sumTokens += float64(result.TokensSaved)
		sumRawPrecision += result.RawPrecisionAtK
		sumRawRecall += result.RawRecallAtK
		sumCorpus += float64(result.CorpusTokens)
		sumContext += float64(result.ContextTokens)
		if result.Err != "" {
			errored++
		}
		if result.RetrievedCount == 0 {
			noResult++
		}
	}

	n := float64(len(cases))
	report.MeanPrecisionAtK = sumPrecision / n
	report.MeanRecallAtK = sumRecall / n
	report.MeanTokensSaved = sumTokens / n
	report.MeanRawPrecisionAtK = sumRawPrecision / n
	report.MeanRawRecallAtK = sumRawRecall / n
	report.RecallDelta = report.MeanRecallAtK - report.MeanRawRecallAtK
	report.MeanCorpusTokens = sumCorpus / n
	report.MeanContextTokens = sumContext / n
	report.EndToEndTokensSaved = report.MeanCorpusTokens - report.MeanContextTokens
	report.NoResultRate = float64(noResult) / n
	report.ErrorRate = float64(errored) / n
	return report
}

// runCase runs one eval case and returns its result.
func (h *EvalHarness) runCase(ctx context.Context, c EvalCase) EvalCaseResult {
	res := EvalCaseResult{Query: c.Query}

	// Raw baseline first: it is the reference the AKG numbers are read against,
	// and it must be measured even when retrieval fails — otherwise a failure
	// would masquerade as a correctness regression.
	res.RawPrecisionAtK, res.RawRecallAtK, res.CorpusTokens = h.rawBaseline(ctx, c)

	if h.retriever == nil {
		res.Err = "eval harness: no retriever wired"
		return res
	}

	query := Query{
		Text:       c.Query,
		Namespace:  c.Namespace,
		MaxResults: 50,
		MaxTokens:  4000,
	}

	result, tier, err := h.retriever.Retrieve(ctx, query)
	res.RetrievalTier = tier.String()
	if err != nil {
		res.Err = err.Error()
		return res
	}

	var graph *knowledge.WorkingGraph
	if result != nil {
		graph = result.Graph
	}
	retrieved := make([]*knowledge.KnowledgeObject, 0)
	if graph != nil {
		for _, obj := range graph.Nodes {
			retrieved = append(retrieved, obj)
		}
	}
	res.RetrievedCount = len(retrieved)
	res.PrecisionAtK, res.RecallAtK = scoreAgainstExpected(retrieved, c.ExpectedIDs)

	// Token accounting: prefer metrics the retriever already attached, and
	// compile on the fly when a compiler is wired (that compile also yields the
	// compiled context size the AKG-vs-raw comparison needs).
	if result != nil && result.Context != nil && result.Context.Metrics.TokensSaved > 0 {
		res.TokensSaved = result.Context.Metrics.TokensSaved
	}
	if h.compiler != nil && graph != nil {
		compiled, cErr := h.compiler.Compile(ctx, graph, compiler.CompileConfig{
			Formats: []compiler.Format{compiler.FormatPrompt},
		})
		if cErr == nil && compiled != nil {
			res.ContextTokens = compiled.Metrics.OutputTokens
			if res.TokensSaved == 0 {
				res.TokensSaved = compiled.Metrics.TokensSaved
			}
		}
	}

	return res
}

// rawBaseline measures the unranked baseline for one case: every object in the
// case's namespace, which is what a consumer feeds the model when it skips AKG
// and uses the raw records directly — the same baseline CompileMetrics.RawTokens
// is defined against. It returns precision, recall and the raw token cost of
// that corpus.
//
// It reports zeros when the baseline cannot be measured (no store, or a case
// with no namespace): an unmeasurable baseline must not be confused with a
// measured one.
func (h *EvalHarness) rawBaseline(ctx context.Context, c EvalCase) (precision, recall float64, corpusTokens int) {
	if h.retriever == nil || h.retriever.store == nil || c.Namespace == "" {
		return 0, 0, 0
	}
	objects, err := h.retriever.store.Query(ctx, knowledge.Query{Namespace: c.Namespace, Limit: 0})
	if err != nil {
		return 0, 0, 0
	}
	precision, recall = scoreAgainstExpected(objects, c.ExpectedIDs)

	if h.compiler != nil && len(objects) > 0 {
		graph := &knowledge.WorkingGraph{Nodes: objectsToMap(objects)}
		compiled, cErr := h.compiler.Compile(ctx, graph, compiler.CompileConfig{
			Formats: []compiler.Format{compiler.FormatPrompt},
		})
		if cErr == nil && compiled != nil {
			corpusTokens = compiled.Metrics.RawTokens
		}
	}
	return precision, recall, corpusTokens
}

// scoreAgainstExpected returns the precision and recall of the retrieved
// objects against the case's expected IDs. Precision is 0 when nothing was
// retrieved, recall is 0 when the case expects nothing.
func scoreAgainstExpected(objects []*knowledge.KnowledgeObject, expectedIDs []string) (precision, recall float64) {
	expected := make(map[string]bool, len(expectedIDs))
	for _, id := range expectedIDs {
		expected[id] = true
	}
	retrieved := make(map[string]bool, len(objects))
	for _, obj := range objects {
		if obj != nil {
			retrieved[obj.ID] = true
		}
	}

	var truePositives int
	for id := range retrieved {
		if expected[id] {
			truePositives++
		}
	}
	if len(retrieved) > 0 {
		precision = float64(truePositives) / float64(len(retrieved))
	}
	if len(expected) > 0 {
		recall = float64(truePositives) / float64(len(expected))
	}
	return precision, recall
}
