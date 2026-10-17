package knowledge

import (
	"regexp"
	"strings"
)

// RelationPattern is one regex → predicate rule used by RelationExtractor.
type RelationPattern struct {
	Predicate   string
	Regex       *regexp.Regexp
	EntityGroup int
}

// RelationExtractor extracts Relations from KnowledgeObject text via a fixed
// rule set (dictionary + regex). It is deliberately non-LLM.
type RelationExtractor struct {
	entityDict map[string]string // alias → canonical entity
	patterns   []RelationPattern
}

// NewRelationExtractor returns an extractor with the default bilingual rule set
// (Chinese + English) for the fixes/depends_on/calls/belongs_to predicates.
//
// Chinese verbs tolerate the aspect particle and direct concatenation (Chinese has
// no word separators), so the separator allows zero or more aspect particles or
// whitespace. English
// verbs require whitespace after the verb and a leading word boundary so that
// "fix" does not match inside words like "prefix" or "fixing".
// entityBound captures one entity and stops it at a conjunction or at sentence
// punctuation, so a multi-clause sentence yields the first clean entity instead
// of the greedy (.+) swallowing the whole remainder (spec section 4: greedy
// regex matched to end of input).
//
// A '.' is deliberately kept OUT of the capture class so dotted identifiers and
// versions ("auth.service", "v1.2.3") survive; it ends the capture only when
// followed by whitespace, i.e. a real sentence boundary.
//
// Every character the capture class excludes MUST also appear in the terminator
// alternation, otherwise the lazy +? has no valid stop position at that
// character and the whole pattern fails to match — "fixes the auth bug!" used
// to extract nothing because '!' sat in the exclusion class but not in the
// terminator. Full-width CJK period/comma/semicolon/colon/exclamation/question
// and their ASCII equivalents therefore all terminate directly. Targets are
// canonicalized against the entity dict downstream, so a clean short entity
// matches far more often than the overlong remainder.
const entityBound = `([^，。；！？,;!?：:]+?)(?:\.\s|[,;:!?]|[，；：！？。]|\s+and\s+|\s+和\s+|\s+与\s+|$)`

func NewRelationExtractor() *RelationExtractor {
	return &RelationExtractor{
		entityDict: make(map[string]string),
		patterns: []RelationPattern{
			// fixes — Chinese and English variants, tolerating the aspect particle.
			{
				Predicate:   RelFixes,
				Regex:       regexp.MustCompile(`修复了?\s*` + entityBound),
				EntityGroup: 1,
			},
			{
				Predicate:   RelFixes,
				Regex:       regexp.MustCompile(`解决了?\s*` + entityBound),
				EntityGroup: 1,
			},
			// fixes — English (\b anchors the verb; \s+ avoids "prefix"/"fixing").
			{
				Predicate:   RelFixes,
				Regex:       regexp.MustCompile(`(?i)\bfix(?:ed|es)?\s+` + entityBound),
				EntityGroup: 1,
			},
			// depends_on — Chinese and English variants of "depends on".
			{
				Predicate:   RelDependsOn,
				Regex:       regexp.MustCompile(`依赖\s*` + entityBound),
				EntityGroup: 1,
			},
			{
				Predicate:   RelDependsOn,
				Regex:       regexp.MustCompile(`(?i)depends?\s+on\s+` + entityBound),
				EntityGroup: 1,
			},
			// calls — Chinese and English variants of "calls".
			{
				Predicate:   RelCalls,
				Regex:       regexp.MustCompile(`调用了?\s*` + entityBound),
				EntityGroup: 1,
			},
			// belongs_to — Chinese and English variants of "belongs to".
			{
				Predicate:   RelBelongsTo,
				Regex:       regexp.MustCompile(`属于\s*` + entityBound),
				EntityGroup: 1,
			},
		},
	}
}

// Extract returns Relations found in the object's Normalized+Summary text.
// Only predicates in AllowedPredicates are kept. It returns an empty (non-nil)
// slice when nothing matched.
//
// Entity capture terminates at punctuation/conjunction boundaries (entityBound)
// so multi-entity sentences yield the first clean entity; targets are matched
// against the entity dict downstream, so overlong captures would simply fail
// to canonicalize.
func (e *RelationExtractor) Extract(obj *KnowledgeObject) []Relation {
	rels := []Relation{}
	if obj == nil {
		return rels
	}
	text := obj.Normalized + " " + obj.Summary
	for _, p := range e.patterns {
		matches := p.Regex.FindAllStringSubmatch(text, -1)
		for _, m := range matches {
			if len(m) <= p.EntityGroup {
				continue
			}
			target := stripTrailingPunct(strings.TrimSpace(m[p.EntityGroup]))
			if target == "" {
				continue
			}
			if canonical, ok := e.entityDict[strings.ToLower(target)]; ok {
				target = canonical
			}
			if !AllowedPredicates[p.Predicate] {
				continue
			}
			rels = append(rels, Relation{
				Predicate:  p.Predicate,
				ObjectText: target,
				Evidence:   m[0],
			})
		}
	}
	return rels
}

// stripTrailingPunct removes trailing punctuation and whitespace so captured
// targets like "auth-bug" or "auth bug," become clean entity text.
func stripTrailingPunct(s string) string {
	s = strings.TrimRight(s, " \t\n\r.,;:。，；：、!?！？")
	return s
}

// TODO(tech-debt): the relation validation gate below (RelationGateConfig /
// ValidateRelation / FilterRelations) has NO production caller yet — nothing
// in the distill/extract path invokes it. Wire it into
// DistillBridge.extractRelations (or the pipeline) before relying on it, or
// delete it. Note wiring it changes extraction behaviour (it drops
// type-incompatible relations), so it should land with its own test.
//
// RelationGateConfig configures the relation validation gate (M2.5).
type RelationGateConfig struct {
	// RequireEvidence, when true, rejects candidate relations that have no
	// evidence span. The RelationExtractor always sets Evidence to the match
	// text, so this only rejects relations added by other paths.
	RequireEvidence bool
}

// DefaultRelationGateConfig returns the default relation gate config:
// evidence not required (backward compatible with pre-0.3.4 relations).
func DefaultRelationGateConfig() RelationGateConfig {
	return RelationGateConfig{
		RequireEvidence: false,
	}
}

// ValidateRelation checks a candidate relation against the M2.5 relation
// validation gate:
//  1. Predicate is in AllowedPredicates.
//  2. Subject type is compatible with the predicate (e.g. "fixes" requires
//     a non-user subject — a user cannot "fix" a bug in the knowledge graph).
//  3. Evidence is present (when RequireEvidence is true).
//  4. The relation does not conflict with existing knowledge (same predicate
//     + same target already present is not a conflict, it is a confirmation;
//     a contradiction would be "fixes" + "causes" on the same target, which
//     is left for the LLM to resolve — the gate only rejects structurally
//     invalid relations).
//
// Returns true when the relation passes the gate, false when it is rejected.
func ValidateRelation(rel Relation, subjectType ObjectType, cfg RelationGateConfig) bool {
	// Step 1: predicate must be in the allowed vocabulary.
	if !AllowedPredicates[rel.Predicate] {
		return false
	}

	// Step 2: subject-predicate type compatibility. Some predicates only make
	// sense for certain subject types. The checks are deliberately permissive:
	// they reject obvious type errors, not edge cases.
	if !isPredicateCompatible(rel.Predicate, subjectType) {
		return false
	}

	// Step 3: evidence requirement (when enabled).
	if cfg.RequireEvidence && rel.Evidence == "" {
		return false
	}

	// Step 4: conflict detection is left to the store-layer dedup; the gate
	// only validates the structural shape of a single candidate relation.
	return true
}

// isPredicateCompatible checks whether the predicate makes semantic sense for
// the given subject type. Unknown predicates (not in the map) are allowed by
// default — the AllowedPredicates check in ValidateRelation already filters
// unknown predicates.
func isPredicateCompatible(predicate string, subjectType ObjectType) bool {
	// No subject type means we cannot check compatibility — allow it.
	if subjectType == "" {
		return true
	}
	switch predicate {
	case RelFixes:
		// "fixes" requires a non-user, non-project subject: typically a
		// commit, decision, or tool result. A "user" cannot "fix" a bug
		// in the knowledge graph sense.
		switch subjectType {
		case ObjectUser, ObjectProject, ObjectDocument:
			return false
		}
		return true
	case RelDependsOn, RelCalls:
		// "depends_on" and "calls" require a technical subject: code,
		// project, commit, or workflow. A "user" or "memory" does not
		// "depend on" or "call" things in the knowledge graph.
		switch subjectType {
		case ObjectUser, ObjectMemory, ObjectDecision:
			return false
		}
		return true
	default:
		// All other predicates (belongs_to, similar_to, supersedes, etc.)
		// are universally applicable.
		return true
	}
}

// FilterRelations applies the relation validation gate to a slice of
// candidate relations, returning only those that pass. The subject type is
// taken from the owning object.
// RelationConflict is a candidate relation that disagrees with what the graph already
// asserts about the same pair of objects.
type RelationConflict struct {
	// Candidate is the relation the gate was asked to admit.
	Candidate Relation
	// Existing is the relation already in the graph that it disagrees with.
	Existing Relation
	// Reason names the disagreement in one line, for whoever must arbitrate it.
	Reason string
}

// relationPairKey identifies the pair a relation is about. The target falls back to the raw
// text when it has not been resolved to a graph object — that is the same identity the
// extraction path uses for an unresolved target, so two assertions about "redis" collide
// whether or not either was resolved.
func relationPairKey(r Relation) string {
	target := r.To
	if target == "" {
		target = r.ObjectText
	}
	return r.From + "\x00" + target
}

// FindRelationConflicts reports candidate relations that disagree with existing ones.
//
// The rule uses the vocabulary's OWN notion of disagreement — the `contradicts` predicate —
// instead of a hand-written list of "single-valued" predicates, which would have to be kept
// in sync with AllowedPredicates forever and would silently rot:
//
//   - the graph already records this pair as contradictory and the candidate asserts
//     something else about it; or
//   - the candidate asserts a contradiction about a pair the graph already asserts.
//
// Both are FLAGS, never rejections. A new source disagreeing with an old one is precisely
// the case a human (or the pipeline's conflict strategy) has to arbitrate; dropping the
// candidate here would erase the disagreement and leave the graph looking unanimous.
//
// It is a separate function rather than another bool on ValidateRelation because a verdict
// cannot carry the pair that disagreed: the caller needs Candidate + Existing to decide,
// and "false" would collapse "incompatible type" and "contradicts the graph" into one
// unactionable no.
func FindRelationConflicts(candidates, existing []Relation) []RelationConflict {
	// The vocabulary stores this predicate as a literal (AllowedPredicates has no constant
	// for it), so the comparison must use the same literal.
	const contradicts = "contradicts"

	byPair := make(map[string][]Relation, len(existing))
	for _, e := range existing {
		byPair[relationPairKey(e)] = append(byPair[relationPairKey(e)], e)
	}

	var out []RelationConflict
	for _, c := range candidates {
		for _, e := range byPair[relationPairKey(c)] {
			switch {
			case e.Predicate == contradicts && c.Predicate != contradicts:
				out = append(out, RelationConflict{
					Candidate: c, Existing: e,
					Reason: "the graph already records this pair as contradictory",
				})
			case c.Predicate == contradicts && e.Predicate != contradicts:
				out = append(out, RelationConflict{
					Candidate: c, Existing: e,
					Reason: "the candidate contradicts a relation the graph already asserts",
				})
			}
		}
	}
	return out
}

func FilterRelations(rels []Relation, subjectType ObjectType, cfg RelationGateConfig) []Relation {
	if len(rels) == 0 {
		return rels
	}
	filtered := rels[:0:0]
	for _, rel := range rels {
		if ValidateRelation(rel, subjectType, cfg) {
			filtered = append(filtered, rel)
		}
	}
	return filtered
}
