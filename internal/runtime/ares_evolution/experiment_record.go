package evolution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ExperimentRecordSchemaVersion is the shape version of ExperimentRecord.
//
// Bump it when fields change meaning; a record without a version cannot be replayed honestly,
// because the reader cannot tell whether an absent field was never recorded or was dropped by
// an older writer.
const ExperimentRecordSchemaVersion = 1

// GateVerdict is one verification gate's conclusion about a candidate.
//
// Reason is recorded, not just the boolean: "rejected" with no reason cannot be re-judged,
// and a replay that only reproduces the verdict but not its grounds proves nothing.
type GateVerdict struct {
	// Gate names the gate that decided (static, evidence, regression, ...).
	Gate string `json:"gate"`
	// Passed reports the verdict.
	Passed bool `json:"passed"`
	// Reason is the gate's own explanation, empty when it passed without comment.
	Reason string `json:"reason,omitempty"`
}

// ExperimentRecord is the durable, replayable account of ONE evolution experiment (M10).
//
// The pieces already exist — genome lineage in the mutator, evidence IDs in the guidance
// path, fitness configuration in the aggregator, verdicts in the lifecycle — but the
// COMBINATION is what makes a result reproducible, and nothing recorded it. Without it a
// surprising outcome can only be argued about, not re-run.
//
// What it deliberately does NOT contain: the outcome as a fitness input. PostDeploy is what
// happened after acceptance, and it is recorded for the reader, not fed back into candidate
// generation — see ExperimentLog for how the loop is cut.
type ExperimentRecord struct {
	// SchemaVersion is ExperimentRecordSchemaVersion at the time of writing.
	SchemaVersion int `json:"schema_version"`

	// Generation is the population generation the candidate came from.
	Generation int `json:"generation"`
	// CandidateID, ParentID and MutationType describe the genome lineage.
	CandidateID  string `json:"candidate_id"`
	ParentID     string `json:"parent_id,omitempty"`
	MutationType string `json:"mutation_type,omitempty"`
	// PromptTemplate and Params are the candidate's BEHAVIOUR. They are recorded because a
	// replay that reconstructs the id but not the behaviour would "reproduce" a different
	// strategy under the same name.
	PromptTemplate string         `json:"prompt_template,omitempty"`
	Params         map[string]any `json:"params,omitempty"`

	// EvidenceIDs are the records that justified narrowing the candidate. They are NOT the
	// fitness source: dimensions come from execution evidence (M9), and conflating the two
	// is how knowledge would start grading candidates.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`

	// FitnessConfig is the configuration in force, recorded as VALUES rather than by
	// reference: a replay must not silently apply today's defaults to yesterday's decision.
	FitnessConfig map[string]float64 `json:"fitness_config,omitempty"`
	// Dimensions are the scores the candidate was ranked on (M9), empty when it had no
	// evidence — absence is recorded as absence, never as zeros.
	Dimensions map[string]float64 `json:"dimensions,omitempty"`
	// Score is the aggregate that selection used.
	Score float64 `json:"score"`

	// Verification is what the gates concluded.
	Verification []GateVerdict `json:"verification,omitempty"`
	// Accepted reports whether the candidate was released.
	Accepted bool `json:"accepted"`

	// PostDeploy is what happened after acceptance. Nil until observed, so "not measured yet"
	// stays distinguishable from "measured as zero".
	PostDeploy *StrategyOutcome `json:"post_deploy,omitempty"`

	// At is when the record was sealed.
	At time.Time `json:"at"`
}

// ReplayKey is a deterministic fingerprint of what DETERMINES the outcome: the candidate's
// behaviour, the evidence that narrowed it, and the fitness configuration that ranked it.
//
// Determinism is the whole point, so map keys are sorted before hashing: Go randomises map
// iteration, and a key that changes between two identical experiments would make every replay
// look like a different one.
//
// Deliberately excluded: Timestamp and Score. Two runs of the same experiment at different
// times must share a key; comparing scores is what a replay is FOR, so folding the result
// into the identity would make "same experiment, different outcome" unrepresentable.
func (r ExperimentRecord) ReplayKey() string {
	h := sha256.New()

	// Line-delimited canonical form: every part on its own line so two records cannot collide
	// by concatenating different fields into the same byte string.
	writeLine := func(parts ...string) {
		for _, p := range parts {
			_, _ = h.Write([]byte(p)) // hash writes never fail
			_, _ = h.Write([]byte{'\n'})
		}
	}

	writeLine("schema", fmt.Sprintf("%d", r.SchemaVersion))
	writeLine("genome", r.CandidateID, r.ParentID, r.MutationType)
	writeLine("behaviour", r.PromptTemplate, canonicalJSON(r.Params))

	ev := append([]string(nil), r.EvidenceIDs...)
	sort.Strings(ev)
	writeLine(append([]string{"evidence"}, ev...)...)

	for _, k := range sortedKeys(r.FitnessConfig) {
		writeLine("fitness", k, fmt.Sprintf("%g", r.FitnessConfig[k]))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON renders v with sorted keys so the fingerprint does not depend on map order.
// A value that cannot be marshalled returns "", which keeps the key stable rather than
// failing a record that is otherwise complete.
func canonicalJSON(v map[string]any) string {
	if len(v) == 0 {
		return ""
	}
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make(map[string]any, len(v))
	for _, k := range keys {
		ordered[k] = v[k]
	}
	raw, err := json.Marshal(ordered)
	if err != nil {
		// Returning "" here would make two DIFFERENT unmarshalable parameter sets share one
		// fingerprint — a replay would then "match" an experiment it never ran. The error text
		// is deterministic for the same input, so distinct inputs stay distinct.
		return fmt.Sprintf("!unmarshalable:%v", err)
	}
	return string(raw)
}

// sortedKeys returns the map's keys in a deterministic order.
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ExperimentLog is an APPEND-ONLY record of experiments.
//
// There is deliberately no Update and no Delete: a record that can be edited after the fact
// cannot prove what was decided, and "which configuration produced this result" is exactly the
// question it exists to answer.
//
// It is also where the self-reinforcing loop is cut (M10). GA may read this log as HISTORY —
// "what did we try, and what happened" — but the outcome of a candidate never becomes an input
// to generating or ranking that same candidate: the log is write-once per experiment, and the
// fitness path reads execution evidence (M9), never this log. Without that separation a
// strategy that scores well once would keep proposing itself.
type ExperimentLog interface {
	// Append records one experiment. Implementations must reject a record with no candidate id
	// rather than storing an unidentifiable entry.
	Append(ctx context.Context, record ExperimentRecord) error
	// List returns the records in append order.
	List(ctx context.Context) ([]ExperimentRecord, error)
}

// MemoryExperimentLog is an in-memory ExperimentLog for wiring paths that have no durable
// store yet, and for tests.
type MemoryExperimentLog struct {
	mu      sync.Mutex // guards records
	records []ExperimentRecord
}

// NewMemoryExperimentLog returns an empty log.
func NewMemoryExperimentLog() *MemoryExperimentLog {
	return &MemoryExperimentLog{}
}

// Append implements ExperimentLog.
func (l *MemoryExperimentLog) Append(_ context.Context, record ExperimentRecord) error {
	if record.CandidateID == "" {
		return fmt.Errorf("experiment log: record has no candidate id")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, cloneRecord(record))
	return nil
}

// List implements ExperimentLog.
//
// It returns DEEP copies, not a slice copy: a shallow copy would still share the maps and
// slices inside each record, so a caller editing record.Params would rewrite what the log
// says was recorded — and "what configuration produced this result" is the one thing an
// append-only log must be able to prove.
func (l *MemoryExperimentLog) List(_ context.Context) ([]ExperimentRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ExperimentRecord, len(l.records))
	for i, r := range l.records {
		out[i] = cloneRecord(r)
	}
	return out, nil
}

// cloneRecord copies the record's reference fields so no caller shares state with the log.
func cloneRecord(r ExperimentRecord) ExperimentRecord {
	out := r
	if r.Params != nil {
		out.Params = make(map[string]any, len(r.Params))
		for k, v := range r.Params {
			out.Params[k] = v
		}
	}
	if r.FitnessConfig != nil {
		out.FitnessConfig = make(map[string]float64, len(r.FitnessConfig))
		for k, v := range r.FitnessConfig {
			out.FitnessConfig[k] = v
		}
	}
	if r.Dimensions != nil {
		out.Dimensions = make(map[string]float64, len(r.Dimensions))
		for k, v := range r.Dimensions {
			out.Dimensions[k] = v
		}
	}
	if r.EvidenceIDs != nil {
		out.EvidenceIDs = append([]string(nil), r.EvidenceIDs...)
	}
	if r.Verification != nil {
		out.Verification = append([]GateVerdict(nil), r.Verification...)
	}
	if r.PostDeploy != nil {
		cloned := *r.PostDeploy
		cloned.ExperienceIDs = append([]string(nil), r.PostDeploy.ExperienceIDs...)
		out.PostDeploy = &cloned
	}
	return out
}
