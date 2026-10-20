package eval

import (
	"context"
	"sync"
)

// AgreementReportingEvaluator decorates an Evaluator with the judge-vs-ground-truth
// readout, so the one number that says how much of the accuracy signal is verifiable
// stops being a method nobody calls.
//
// It exists because the whole P0-1(ii) chain ends in a scalar that the GA selects on,
// and the trustworthy half of that scalar is the part deterministic checks anchored.
// Without a reader, a judge that drifts toward crediting wrong answers keeps producing
// a plausible number forever — the drift is only visible in the counters, and counters
// nobody logs are not observability.
//
// Reporting is per STATE CHANGE, not per case: a suite is scored case by case, and a
// line per case would be a line per case forever. The state is the count tuple itself,
// so any movement (a new anchored case, a lenient disagreement, a broken declaration)
// speaks up while a stable run stays quiet.
type AgreementReportingEvaluator struct {
	inner    Evaluator
	reporter groundTruthAgreementReporter

	mu       sync.Mutex // guards last and reported
	last     GroundTruthAgreement
	reported bool
}

// NewAgreementReportingEvaluator wraps an evaluator that keeps judge-vs-ground-truth
// counters. An evaluator without them is returned UNWRAPPED: decor with nothing to
// report is a layer of indirection that can only hide the inner type.
func NewAgreementReportingEvaluator(inner Evaluator) Evaluator {
	reporter, ok := inner.(groundTruthAgreementReporter)
	if !ok {
		return inner
	}
	return &AgreementReportingEvaluator{inner: inner, reporter: reporter}
}

// Evaluate delegates the scoring and then reports the counters, unchanged or not.
//
// The error is passed through untouched: this decorator reports, it never decides.
func (e *AgreementReportingEvaluator) Evaluate(ctx context.Context, tc TestCase, result TestResult) ([]EvalScore, error) {
	scores, err := e.inner.Evaluate(ctx, tc, result)
	e.report()
	return scores, err
}

// report writes the readout when the counters moved since the last call.
func (e *AgreementReportingEvaluator) report() {
	stats := e.reporter.GroundTruthAgreement()

	e.mu.Lock()
	unchanged := e.reported && stats == e.last
	e.last, e.reported = stats, true
	e.mu.Unlock()
	if unchanged {
		return
	}
	if stats.Anchored == 0 && stats.Broken == 0 {
		// "Nothing anchored yet" is not news: every run starts here, and a judge-only
		// suite stays here forever. What the readout exists for is the MOVEMENT — the
		// first anchored case, the first leniency, the first broken declaration.
		return
	}

	// A declared check the layer cannot resolve is a corpus bug: the case silently
	// loses its anchor, so it is called out separately rather than folded into a
	// total that still looks healthy.
	if stats.Broken > 0 {
		log.Warn("eval: cases declared checks the deterministic layer could not resolve",
			"anchored", stats.Anchored, "broken", stats.Broken)
	}
	log.Info("eval: judge vs ground truth",
		"anchored", stats.Anchored,
		"lenient", stats.Lenient,
		"harsh", stats.Harsh,
		"broken", stats.Broken,
		"leniency_rate", stats.LeniencyRate())
}
