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
// Chinese verbs tolerate the 了 particle and direct concatenation (Chinese has
// no word separators), so the separator is `(?:了|\s)*` (zero or more). English
// verbs require whitespace after the verb and a leading word boundary so that
// "fix" does not match inside words like "prefix" or "fixing".
func NewRelationExtractor() *RelationExtractor {
	return &RelationExtractor{
		entityDict: make(map[string]string),
		patterns: []RelationPattern{
			// fixes — Chinese (修复/解决), tolerating the 了 particle.
			{
				Predicate:   RelFixes,
				Regex:       regexp.MustCompile(`修复了?\s*(.+)`),
				EntityGroup: 1,
			},
			{
				Predicate:   RelFixes,
				Regex:       regexp.MustCompile(`解决了?\s*(.+)`),
				EntityGroup: 1,
			},
			// fixes — English (\b anchors the verb; \s+ avoids "prefix"/"fixing").
			{
				Predicate:   RelFixes,
				Regex:       regexp.MustCompile(`(?i)\bfix(?:ed|es)?\s+(.+)`),
				EntityGroup: 1,
			},
			// depends_on — Chinese 依赖 + English "depends on".
			{
				Predicate:   RelDependsOn,
				Regex:       regexp.MustCompile(`依赖\s*(.+)`),
				EntityGroup: 1,
			},
			{
				Predicate:   RelDependsOn,
				Regex:       regexp.MustCompile(`(?i)depends?\s+on\s+(.+)`),
				EntityGroup: 1,
			},
			// calls — Chinese 调用.
			{
				Predicate:   RelCalls,
				Regex:       regexp.MustCompile(`调用了?\s*(.+)`),
				EntityGroup: 1,
			},
			// belongs_to — Chinese 属于.
			{
				Predicate:   RelBelongsTo,
				Regex:       regexp.MustCompile(`属于\s*(.+)`),
				EntityGroup: 1,
			},
		},
	}
}

// Extract returns Relations found in the object's Normalized+Summary text.
// Only predicates in AllowedPredicates are kept. It returns an empty (non-nil)
// slice when nothing matched.
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
// targets like "鉴权bug。" or "auth bug," become clean entity text.
func stripTrailingPunct(s string) string {
	s = strings.TrimRight(s, " \t\n\r.,;:。，；：、!?！？")
	return s
}
