// Package compiler — RuleNormalizer implements the Normalize stage with
// deterministic, rule-based alias resolution and coreference collapse
// (zero LLM token cost).
package compiler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// errEmptyCanonical signals that an alias was mapped to an empty canonical name.
var errEmptyCanonical = errors.New("empty canonical name")

// RuleNormalizer canonicalizes extracted entities and facts using rule-based
// alias resolution, coreference collapse, and name canonicalization. It never
// calls an LLM — all normalization is deterministic string/rule processing.
type RuleNormalizer struct {
	aliases map[string]string // lowercase alias -> canonical name
	mu      sync.RWMutex
}

// NewRuleNormalizer creates a RuleNormalizer seeded with a default alias table
// covering common programming-language and system-name variants.
func NewRuleNormalizer() *RuleNormalizer {
	return &RuleNormalizer{
		aliases: defaultAliasTable(),
	}
}

// NewRuleNormalizerWithAliases creates a RuleNormalizer with extra user-provided
// aliases merged on top of the defaults. Each entry maps an alias (lowercase)
// to its canonical name. Returns error if a canonical name is empty.
func NewRuleNormalizerWithAliases(extra map[string]string) (*RuleNormalizer, error) {
	aliases := defaultAliasTable()
	for k, v := range extra {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		canonical := strings.TrimSpace(v)
		if canonical == "" {
			return nil, fmt.Errorf("rule normalizer: alias %q: %w", k, errEmptyCanonical)
		}
		aliases[key] = canonical
	}
	return &RuleNormalizer{aliases: aliases}, nil
}

// Normalize implements Normalizer. Pipeline:
//  1. Canonicalize each entity name via the alias table (case-insensitive).
//  2. Collapse coreferences: entities that canonicalize to the same name merge
//     (union aliases, max confidence, union properties).
//  3. Canonicalize fact subject/object to the same canonical names.
//  4. Deduplicate facts by (canonical subject, predicate, canonical object),
//     keeping the highest confidence.
//  5. Drop entities whose canonical name is empty after trimming.
func (n *RuleNormalizer) Normalize(ctx context.Context, entities []ExtractedEntity, facts []ExtractedFact) ([]ExtractedEntity, []ExtractedFact, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("rule normalizer: context cancelled: %w", err)
		}
	}
	normalizedEntities := n.normalizeEntities(entities)
	normalizedFacts := n.normalizeFacts(facts)
	return normalizedEntities, normalizedFacts, nil
}

// Name returns "rule" as the normalizer identifier.
func (n *RuleNormalizer) Name() string { return "rule" }

// normalizeEntities canonicalizes entity names and collapses coreferences that
// resolve to the same canonical name. Entities with empty canonical names are
// dropped. First-seen order of canonical names is preserved.
func (n *RuleNormalizer) normalizeEntities(entities []ExtractedEntity) []ExtractedEntity {
	if len(entities) == 0 {
		return []ExtractedEntity{}
	}
	groups := make(map[string]*ExtractedEntity, len(entities))
	order := make([]string, 0, len(entities))
	for _, e := range entities {
		canonName := n.canonicalize(e.Name)
		if strings.TrimSpace(canonName) == "" {
			continue
		}
		key := strings.ToLower(canonName)
		if existing, ok := groups[key]; ok {
			mergeEntity(existing, e, canonName)
			continue
		}
		merged := e
		merged.Name = canonName
		merged.Aliases = unionAliases(canonName, merged.Aliases)
		merged.Properties = copyProperties(merged.Properties)
		groups[key] = &merged
		order = append(order, key)
	}
	result := make([]ExtractedEntity, 0, len(order))
	for _, key := range order {
		result = append(result, *groups[key])
	}
	return result
}

// normalizeFacts canonicalizes fact subject and object via the alias table, then
// deduplicates facts by (canonical subject, predicate, canonical object), keeping
// the highest confidence on conflict. First-seen order of keys is preserved.
func (n *RuleNormalizer) normalizeFacts(facts []ExtractedFact) []ExtractedFact {
	if len(facts) == 0 {
		return []ExtractedFact{}
	}
	best := make(map[string]int, len(facts))
	var result []ExtractedFact
	for _, f := range facts {
		f.Subject = n.canonicalize(f.Subject)
		f.Object = n.canonicalize(f.Object)
		key := f.Subject + "|" + f.Predicate + "|" + f.Object
		if idx, ok := best[key]; ok {
			if f.Confidence > result[idx].Confidence {
				result[idx] = f
			}
			continue
		}
		best[key] = len(result)
		result = append(result, f)
	}
	return result
}

// canonicalize trims and collapses whitespace, then resolves the name through
// the alias table (case-insensitive). Names not present in the table are
// returned as the trimmed original; no blind title-casing is applied.
func (n *RuleNormalizer) canonicalize(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.Join(strings.Fields(trimmed), " ")
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	n.mu.RLock()
	canonical := n.aliases[lower]
	n.mu.RUnlock()
	if canonical != "" {
		return canonical
	}
	return trimmed
}

// mergeEntity merges src into dst using coreference collapse rules:
// max confidence, first non-empty SourceID, unioned aliases (excluding the
// canonical name), and merged properties (src wins on key conflict).
func mergeEntity(dst *ExtractedEntity, src ExtractedEntity, canonicalName string) {
	if src.Confidence > dst.Confidence {
		dst.Confidence = src.Confidence
	}
	if dst.SourceID == "" && src.SourceID != "" {
		dst.SourceID = src.SourceID
	}
	dst.Aliases = unionAliases(canonicalName, dst.Aliases, src.Aliases)
	if len(src.Properties) == 0 {
		return
	}
	if dst.Properties == nil {
		dst.Properties = make(map[string]string, len(src.Properties))
	}
	for k, v := range src.Properties {
		dst.Properties[k] = v
	}
}

// unionAliases returns the case-insensitive deduplication of the given alias
// slices, excluding any alias equal to canonicalName. Order is preserved by
// first occurrence.
func unionAliases(canonicalName string, aliasSlices ...[]string) []string {
	seen := make(map[string]bool)
	seen[strings.ToLower(canonicalName)] = true
	var result []string
	for _, aliases := range aliasSlices {
		for _, a := range aliases {
			if a == canonicalName {
				continue
			}
			la := strings.ToLower(a)
			if seen[la] {
				continue
			}
			seen[la] = true
			result = append(result, a)
		}
	}
	return result
}

// copyProperties returns a shallow copy of the given properties map. Returns
// nil for nil or empty input so the omitted JSON field stays omitted.
func copyProperties(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// defaultAliasTable returns a fresh alias table covering common
// programming-language and system-name variants, including known language
// names that should be title-cased when encountered as lowercase identifiers.
const (
	langRust       = "Rust"
	langGo         = "Go"
	langTypeScript = "TypeScript"
	langJavaScript = "JavaScript"
	langPython     = "Python"
	dbPostgreSQL   = "PostgreSQL"
	platformK8s    = "Kubernetes"
	abbrLLM        = "LLM"
	// Alias key constants (repeated across alias table and tests).
	aliasRust       = "rust"
	aliasGolang     = "golang"
	aliasK8s        = "k8s"
	aliasKubernetes = "kubernetes"
	aliasLLM        = "llm"
)

func defaultAliasTable() map[string]string {
	return map[string]string{
		// Required aliases.
		"rust语言":        langRust,
		aliasRust:       langRust,
		aliasGolang:     langGo,
		"go语言":          langGo,
		"ts":            langTypeScript,
		"typescript":    langTypeScript,
		"js":            langJavaScript,
		"javascript":    langJavaScript,
		"py":            langPython,
		"python":        langPython,
		"pg":            dbPostgreSQL,
		"postgres":      dbPostgreSQL,
		"postgresl":     dbPostgreSQL, // common typo
		aliasK8s:        platformK8s,
		aliasKubernetes: platformK8s,
		aliasLLM:        abbrLLM,
		// Known language set: title-case lowercase language identifiers.
		"go":     "Go",
		"java":   "Java",
		"ruby":   "Ruby",
		"kotlin": "Kotlin",
		"swift":  "Swift",
		"scala":  "Scala",
		"php":    "PHP",
		"perl":   "Perl",
		"sql":    "SQL",
		"html":   "HTML",
		"css":    "CSS",
		"json":   "JSON",
		"yaml":   "YAML",
		"xml":    "XML",
		"bash":   "Bash",
		"shell":  "Shell",
		"c":      "C",
		"c++":    "C++",
		"c#":     "C#",
	}
}

// Ensure compile-time interface check.
var _ Normalizer = (*RuleNormalizer)(nil)
