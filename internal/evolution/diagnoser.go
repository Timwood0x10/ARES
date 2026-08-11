package evolution

import (
	"context"
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// MinFailureClusterSize is the minimum number of same-role failure records
// required before a candidate is generated (Ch.8: repeated failure patterns
// indicate a systemic capability gap, not a one-off).
const MinFailureClusterSize = 2

// ErrEvidenceStoreNil is returned when the diagnoser has no evidence store.
var ErrEvidenceStoreNil = errors.New("evolution: diagnoser has nil evidence store")

// Diagnoser generates evolution candidates from failure evidence clusters.
// It answers "which role repeatedly fails, and how" and packages that into a
// Candidate for the verification pipeline. Candidate content (diff/reason) is
// provided by a developer or human reviewer — no automatic LLM generation in
// v1 (Ch.8: candidate generation must stay within a bounded harness).
type Diagnoser struct {
	store evidence.Store
}

// NewDiagnoser creates a diagnoser that queries the given evidence store for
// failure clusters.
// Args:
//
//	store - the universal evidence store; must be non-nil.
//
// Returns:
//
//	diagnoser - the ready-to-use diagnoser.
func NewDiagnoser(store evidence.Store) *Diagnoser {
	return &Diagnoser{store: store}
}

// GenerateRequest carries the human-confirmed candidate content and the role
// whose failures triggered the candidate.
type GenerateRequest struct {
	// Role is the agent role to improve, e.g. "coder".
	Role string

	// Diff is the new Instructions text (candidate change), confirmed by a
	// developer or reviewer.
	Diff string

	// Reason explains why this change is needed; references the failure pattern.
	Reason string
}

// Generate queries failure evidence for the role and produces a Candidate when
// at least MinFailureClusterSize failing records exist.
//
// Args:
//
//	ctx - timeout and cancellation context.
//	req - human-confirmed candidate content; Role and Diff must be non-empty.
//
// Returns:
//
//	candidate - a new candidate in StatusCandidate, or nil when the failure
//	  cluster is smaller than MinFailureClusterSize.
//	err - ErrEvidenceStoreNil when the store is nil, or a validation error.
func (d *Diagnoser) Generate(ctx context.Context, req GenerateRequest) (*Candidate, error) {
	if d.store == nil {
		return nil, ErrEvidenceStoreNil
	}
	if req.Role == "" {
		return nil, errors.New("evolution: diagnose role must not be empty")
	}
	if req.Diff == "" {
		return nil, errors.New("evolution: diagnose diff must not be empty")
	}

	// Query failure-cluster evidence for the role. The dimension_eval kind is
	// produced by the three-layer verifiers via the evidence bridge (P0-4).
	records, err := d.store.Query(ctx, evidence.Filter{
		Kind:   evidence.KindDimensionEval,
		Source: "result_verifier",
	})
	if err != nil {
		return nil, fmt.Errorf("evolution: query failure evidence: %w", err)
	}

	// Count failures belonging to the target role. The role is carried in the
	// evidence metadata written by the bridge (role=...).
	var evidenceIDs []string
	for _, record := range records {
		if record.Metadata["role"] != req.Role {
			continue
		}
		evidenceIDs = append(evidenceIDs, record.ID)
	}

	if len(evidenceIDs) < MinFailureClusterSize {
		//nolint:nilnil // nil candidate + nil error is the documented "no systemic pattern yet" contract.
		return nil, nil
	}

	return NewCandidate(CandidateInstruction, req.Role, req.Diff, req.Reason, evidenceIDs), nil
}
