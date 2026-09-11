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
// entityBound terminates a captured entity at a conjunction or sentence
// punctuation so "修复了 A，B" / "fixes A and B" / "fixes the auth bug. See
// also..." yield the first entity only, instead of the greedy (.+) swallowing
// the whole remainder (§四: "贪婪正则匹配到输入末尾"). A '.' is deliberately
// kept OUT of the capture class so dotted identifiers/versions
// ("auth.service", "v1.2.3") survive; a '.' ends the capture only when
// followed by whitespace (a real sentence boundary). Full-width 。，；！？ and
// ASCII , ; : ! ? terminate directly. Targets are canonicalized against the
// entity dict downstream, so a clean short entity matches far more often than
// the overlong remainder.
const entityBound = `([^，。；！？,;!?：:]+?)(?:\.\s|,|，|;|；|:|：|\s+and\s+|\s+和\s+|\s+与\s+|$)`

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
