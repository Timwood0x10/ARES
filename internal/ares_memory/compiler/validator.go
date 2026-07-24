// Package compiler — quality gate for AKG projection.
//
// ValidateNodeForAKG performs zero-LLM structural checks on a KM node before
// it is projected into the AKG knowledge graph. Its job is to keep
// structurally invalid nodes (incomplete triples, stopword entities,
// degenerate summaries) out of the retrieval graph that feeds agent
// responses. Confidence filtering is a separate concern, handled later by the
// AKG selector via AKGMinConfidence.
package compiler

import "strings"

// akgStopwords are filler words that carry no extractable meaning on their
// own. A node whose only content is one of these is structurally invalid and
// must not enter the AKG retrieval graph.
var akgStopwords = map[string]struct{}{
	"a": {}, "an": {}, "of": {}, "to": {}, "in": {}, "on": {},
	"for": {}, "and": {}, "or": {}, "is": {}, "are": {}, "be": {}, "by": {},
	"with": {}, "at": {}, "as": {}, "it": {}, "this": {}, "that": {}, "the": {},
	// Chinese fillers.
	"的": {}, "了": {}, "是": {}, "在": {}, "和": {}, "与": {}, "也": {}, "就": {}, "都": {}, "而": {},
}

// rejectReason constants keep quality-gate rejection reasons consistent
// across the validator and its tests (and satisfy goconst).
const (
	reasonStopwordEntity = "stopword entity"
)

// ValidateNodeForAKG reports whether a KM node is structurally fit to be
// projected into the AKG knowledge graph.
//
// The gate is deliberately conservative: it rejects only structurally invalid
// nodes, not merely low-confidence ones. Dropping on confidence alone would
// discard useful but weakly-signaled knowledge; confidence ranking is the
// selector's responsibility downstream.
//
// Args:
//
//	n — node to validate; must not be nil.
//
// Returns:
//
//	ok     — true if the node may be projected.
//	reason — rejection reason when ok is false ("" when ok).
func ValidateNodeForAKG(n *Node) (ok bool, reason string) {
	if n == nil {
		return false, "nil node"
	}
	if n.Type == "" {
		return false, "empty node type"
	}
	if n.Confidence < 0 {
		return false, "negative confidence"
	}
	switch n.Type {
	case NodeMemory:
		// Curated distilled memory is never dropped on structural grounds;
		// the distiller already vetted it.
		return true, ""
	case NodeFact, NodeReference:
		subj := strings.TrimSpace(attrString(n, attrSubject))
		pred := strings.TrimSpace(attrString(n, attrPredicate))
		obj := strings.TrimSpace(attrString(n, attrObject))
		if subj == "" || pred == "" || obj == "" {
			return false, "incomplete triple"
		}
		if isStopwordRun(subj) || isStopwordRun(pred) || isStopwordRun(obj) {
			return false, "stopword triple"
		}
	case NodeEntity:
		name := strings.TrimSpace(attrString(n, attrName))
		if name == "" {
			return false, "empty entity name"
		}
		// An entity whose name is entirely stopwords carries no retrievable
		// meaning and would be indexed under an empty key, polluting AKG
		// retrieval/dedup. Drop it. This catches single tokens such as "the"
		// and multi-token filler like "of the" — both normalize to an empty
		// key and must never enter the knowledge graph.
		if entityResidual(name) == "" {
			return false, reasonStopwordEntity
		}
	default:
		// decision / constraint / tradeoff / question / goal / task /
		// evidence: require actual extracted content. nodeSummary falls back
		// to the node ID when nothing was extracted, so a summary equal to
		// the ID means the node carries no meaning and must be dropped.
		summary := nodeSummary(n)
		if summary == n.ID {
			return false, "no extracted content"
		}
		if isPurePunctuation(summary) {
			return false, "degenerate summary"
		}
	}
	return true, ""
}

// isStopwordRun reports whether s is empty or consists solely of a known
// filler word, i.e. it carries no extractable meaning.
func isStopwordRun(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return true
	}
	_, ok := akgStopwords[s]
	return ok
}

// entityResidual returns name with every stopword token removed. An entity
// whose name collapses to the empty string after this is pure filler (e.g.
// "the", "of the") and must not be projected into the AKG graph: it would
// otherwise be indexed with an empty key and pollute retrieval/dedup.
func entityResidual(name string) string {
	fields := strings.Fields(strings.ToLower(name))
	kept := make([]string, 0, len(fields))
	for _, f := range fields {
		if _, ok := akgStopwords[f]; ok {
			continue
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " ")
}

// isPurePunctuation reports whether s contains no alphanumeric or CJK content.
func isPurePunctuation(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return true
	}
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || (r >= 0x4E00 && r <= 0x9FFF) {
			return false
		}
	}
	return true
}
