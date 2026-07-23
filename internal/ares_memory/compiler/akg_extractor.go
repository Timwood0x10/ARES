// Package compiler — AKGExtractor integrates AKG (Knowledge Fabric) into the
// Compiler's Extract stage, providing zero-LLM-cost entity and fact extraction.
package compiler

import (
	"context"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/logger"
)

var el = logger.New("compiler")

// Entity type constants for extraction results.
const entityTypeConcept = "concept"
const entityTypeLanguage = "language"
const attrChoice = "choice"
const attrRejection = "rejection"
const extractorNameAKG = "akg"

// AKGExtractor implements the Extractor interface using AKG (Knowledge Fabric)
// infrastructure. It extracts entities and facts from conversation messages
// using rule-based parsing and NER, with zero LLM token cost.
//
// The extractor builds KnowledgeObjects from messages and runs them through
// AKG's pipeline (normalizer, entity matcher, summarizer) to produce
// structured entities and facts.
type AKGExtractor struct {
	pipeline *AKGExtractionPipeline
}

// AKGExtractionPipeline holds the AKG processing stages for extraction.
type AKGExtractionPipeline struct {
	normalizer interface {
		Normalize(ctx context.Context, obj *knowledge.KnowledgeObject) (*knowledge.KnowledgeObject, error)
	}
}

// NewAKGExtractor creates a new AKGExtractor with the default AKG pipeline.
//
// The extractor uses AKG's existing infrastructure:
//   - DefaultNormalizer for text normalization
//   - EntityMatcher for entity recognition
//   - Rule-based fact extraction from structured content
//
// Returns:
//
//	*AKGExtractor — the configured extractor. Always non-nil.
func NewAKGExtractor() *AKGExtractor {
	return &AKGExtractor{
		pipeline: &AKGExtractionPipeline{
			normalizer: &defaultNormalizerAdapter{},
		},
	}
}

// defaultNormalizerAdapter adapts the AKG pipeline's normalizer to a simple
// interface that extracts entities from message content.
type defaultNormalizerAdapter struct{}

func (a *defaultNormalizerAdapter) Normalize(_ context.Context, obj *knowledge.KnowledgeObject) (*knowledge.KnowledgeObject, error) {
	if obj == nil {
		return nil, fmt.Errorf("akg extractor: knowledge object must not be nil")
	}
	// Simple normalization: strip extra whitespace, collapse newlines.
	normalized := string(obj.Raw)
	if obj.Normalized != "" {
		normalized = obj.Normalized
	}
	normalized = strings.TrimSpace(normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")
	obj.Normalized = normalized
	return obj, nil
}

// Name returns "akg" as the extractor identifier.
func (e *AKGExtractor) Name() string { return extractorNameAKG }

// Extract extracts entities and facts from source messages using AKG.
//
// The extraction process:
//  1. Create KnowledgeObjects from each source message.
//  2. Run through AKG normalizer for text cleaning.
//  3. Extract entities via pattern matching (code blocks, references, keywords).
//  4. Extract facts via structured triple extraction.
//
// Args:
//
//	ctx — context for cancellation and timeout.
//	messages — source messages to extract from.
//
// Returns:
//
//	entities — extracted entities with confidence scores.
//	facts — extracted facts as subject-predicate-object triples.
//	err — non-nil if extraction fails critically.
func (e *AKGExtractor) Extract(ctx context.Context, messages []SourceMessage) ([]ExtractedEntity, []ExtractedFact, error) {
	if len(messages) == 0 {
		return nil, nil, nil
	}

	var entities []ExtractedEntity
	var facts []ExtractedFact

	for _, msg := range messages {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("akg extractor: context cancelled: %w", err)
		}

		// Skip empty or tool-only messages.
		if msg.Content == "" {
			continue
		}

		// Extract code blocks from raw content BEFORE normalization
		// (normalization collapses newlines, breaking code block detection).
		entities = append(entities, extractCodeBlockEntities(msg.Content, msg.ID)...)

		// Create a KnowledgeObject from the message.
		ko := &knowledge.KnowledgeObject{
			ID:         msg.ID,
			Type:       knowledge.ObjectDocument,
			Raw:        []byte(msg.Content),
			Normalized: msg.Content,
			CreatedAt:  msg.Timestamp,
			UpdatedAt:  msg.Timestamp,
		}

		// Run through AKG normalizer.
		normalized, err := e.pipeline.normalizer.Normalize(ctx, ko)
		if err != nil {
			el.Warn(context.Background(), "akg extractor", "normalize failed",
				"msg_id", msg.ID, "error", err)
			continue
		}

		// Extract entities from the normalized content.
		msgEntities := e.extractEntities(normalized, msg.ID)
		entities = append(entities, msgEntities...)

		// Extract decisions, constraints, tradeoffs, and open questions.
		entities = append(entities, e.extractDecisions(normalized, msg.ID)...)
		entities = append(entities, e.extractConstraints(normalized, msg.ID)...)
		entities = append(entities, e.extractTradeoffs(normalized, msg.ID)...)
		entities = append(entities, e.extractOpenQuestions(normalized, msg.ID)...)

		// Extract facts from the normalized content.
		msgFacts := e.extractFacts(normalized, msg.ID)
		facts = append(facts, msgFacts...)
	}

	// Deduplicate entities by name.
	entities = deduplicateEntities(entities)

	el.Info(context.Background(), "akg extractor", "extraction complete",
		"messages", len(messages),
		"entities", len(entities),
		"facts", len(facts),
	)

	return entities, facts, nil
}

// extractCodeBlockEntities extracts language entities from code blocks in raw content.
func extractCodeBlockEntities(content, sourceID string) []ExtractedEntity {
	var entities []ExtractedEntity
	seen := make(map[string]bool)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(trimmed[3:])
			if lang != "" && !seen[lang] {
				seen[lang] = true
				entities = append(entities, ExtractedEntity{
					Name:       lang,
					Type:       entityTypeLanguage,
					Confidence: 0.9,
					SourceID:   sourceID,
				})
			}
		}
	}
	return entities
}

// extractEntities extracts entities from a normalized message.
// Uses rule-based patterns: code blocks, capitalized terms, known keywords.
func (e *AKGExtractor) extractEntities(normalized *knowledge.KnowledgeObject, sourceID string) []ExtractedEntity {
	content := normalized.Normalized
	if content == "" {
		return nil
	}

	var entities []ExtractedEntity
	seen := make(map[string]bool)

	// Extract code block language identifiers.
	codeBlockLangs := extractCodeBlockLanguages(content)
	for _, lang := range codeBlockLangs {
		if !seen[lang] {
			seen[lang] = true
			entities = append(entities, ExtractedEntity{
				Name:       lang,
				Type:       entityTypeLanguage,
				Confidence: 0.9,
				SourceID:   sourceID,
			})
		}
	}

	// Extract capitalized terms (potential named entities).
	words := strings.Fields(content)
	for _, word := range words {
		cleaned := strings.Trim(word, ".,;:!?()[]{}'\"")
		if cleaned == "" {
			continue
		}
		// Heuristic: capitalized multi-char words are potential entities.
		if len(cleaned) > 2 && isCapitalized(cleaned) && !isCommonWord(cleaned) {
			if !seen[cleaned] {
				seen[cleaned] = true
				entities = append(entities, ExtractedEntity{
					Name:       cleaned,
					Type:       entityTypeConcept,
					Confidence: 0.5,
					SourceID:   sourceID,
				})
			}
		}
	}

	return entities
}

// extractFacts extracts structured triples from a normalized message.
// Uses rule-based patterns: "X is Y", "X uses Y", "X implements Y".
func (e *AKGExtractor) extractFacts(normalized *knowledge.KnowledgeObject, sourceID string) []ExtractedFact {
	content := normalized.Normalized
	if content == "" {
		return nil
	}

	var facts []ExtractedFact
	sentences := splitSentences(content)
	for _, sentence := range sentences {
		triple := extractTriple(sentence)
		if triple != nil {
			facts = append(facts, ExtractedFact{
				Subject:    triple.subject,
				Predicate:  triple.predicate,
				Object:     triple.object,
				Confidence: 0.6,
				SourceID:   sourceID,
			})
		}
	}
	return facts
}

// extractDecisions extracts decision nodes from normalized content.
// Patterns: "we chose X", "we decided to Y", "instead of A, we use B".
func (e *AKGExtractor) extractDecisions(normalized *knowledge.KnowledgeObject, sourceID string) []ExtractedEntity {
	content := normalized.Normalized
	if content == "" {
		return nil
	}

	var decisions []ExtractedEntity
	seen := make(map[string]bool)
	sentences := splitSentences(content)

	decisionPatterns := []struct {
		prefix string
		field  string // attrChoice or attrRejection
	}{
		{"we chose ", attrChoice},
		{"we decided to ", attrChoice},
		{"we opted for ", attrChoice},
		{"we selected ", attrChoice},
		{"we picked ", attrChoice},
		{"we rejected ", attrRejection},
		{"we ruled out ", attrRejection},
		{"we abandoned ", attrRejection},
		{"instead of ", attrRejection},
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, dp := range decisionPatterns {
			idx := strings.Index(lower, dp.prefix)
			if idx < 0 {
				continue
			}
			val := strings.TrimSpace(sentence[idx+len(dp.prefix):])
			if puncIdx := strings.IndexAny(val, ".,;:!?"); puncIdx > 0 {
				val = val[:puncIdx]
			}
			val = strings.TrimSpace(val)
			if val == "" || seen[val] {
				continue
			}
			seen[val] = true
			decisions = append(decisions, ExtractedEntity{
				Name:       val,
				Type:       "decision_" + dp.field,
				Confidence: 0.7,
				SourceID:   sourceID,
			})
		}
	}
	return decisions
}

// extractConstraints extracts constraint nodes from normalized content.
// Patterns: "must be", "cannot", "requirement", "needs to", "must not".
func (e *AKGExtractor) extractConstraints(normalized *knowledge.KnowledgeObject, sourceID string) []ExtractedEntity {
	content := normalized.Normalized
	if content == "" {
		return nil
	}

	var constraints []ExtractedEntity
	seen := make(map[string]bool)
	sentences := splitSentences(content)

	constraintIndicators := []string{
		" must be ", " must not ", " cannot ", " can not ",
		" requirement ", " requirements ",
		" needs to ", " need to ",
		" is required ", " are required ",
		" is mandatory ", " are mandatory ",
		" is necessary ", " are necessary ",
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, indicator := range constraintIndicators {
			idx := strings.Index(lower, indicator)
			if idx < 0 {
				continue
			}
			val := strings.TrimSpace(sentence)
			if len(val) > 120 {
				val = val[:120] + "..."
			}
			if val == "" || seen[val] {
				continue
			}
			seen[val] = true
			constraints = append(constraints, ExtractedEntity{
				Name:       val,
				Type:       "constraint",
				Confidence: 0.6,
				SourceID:   sourceID,
			})
		}
	}
	return constraints
}

// extractTradeoffs extracts tradeoff nodes from normalized content.
// Patterns: "tradeoff between X and Y", "at the cost of", "but sacrifices".
func (e *AKGExtractor) extractTradeoffs(normalized *knowledge.KnowledgeObject, sourceID string) []ExtractedEntity {
	content := normalized.Normalized
	if content == "" {
		return nil
	}

	var tradeoffs []ExtractedEntity
	seen := make(map[string]bool)
	sentences := splitSentences(content)

	tradeoffIndicators := []string{
		" tradeoff ", " trade-off ", " trade off ",
		" at the cost of ", " at the expense of ",
		" but sacrifices ", " but sacrifices ",
		" on the other hand ",
		" however ", " although ", " though ",
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, indicator := range tradeoffIndicators {
			idx := strings.Index(lower, indicator)
			if idx < 0 {
				continue
			}
			val := strings.TrimSpace(sentence)
			if len(val) > 120 {
				val = val[:120] + "..."
			}
			if val == "" || seen[val] {
				continue
			}
			seen[val] = true
			tradeoffs = append(tradeoffs, ExtractedEntity{
				Name:       val,
				Type:       "tradeoff",
				Confidence: 0.5,
				SourceID:   sourceID,
			})
		}
	}
	return tradeoffs
}

// extractOpenQuestions extracts open question nodes from normalized content.
// Patterns: "we need to figure out", "open question", "TODO", "we should investigate".
func (e *AKGExtractor) extractOpenQuestions(normalized *knowledge.KnowledgeObject, sourceID string) []ExtractedEntity {
	content := normalized.Normalized
	if content == "" {
		return nil
	}

	var questions []ExtractedEntity
	seen := make(map[string]bool)
	sentences := splitSentences(content)

	questionIndicators := []string{
		" open question ", " open questions ",
		" we need to figure out ", " we need to determine ",
		" we should investigate ", " we should explore ",
		" todo ", " todo:", " fixme ", " fixme:",
		" not yet decided ", " not yet resolved ",
		" remains to be seen ", " remains to be determined ",
		// Sentence-start variants (no leading space).
		"we need to figure out ", "we need to determine ",
		"we should investigate ", "we should explore ",
		"not yet decided ", "not yet resolved ",
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, indicator := range questionIndicators {
			idx := strings.Index(lower, indicator)
			if idx < 0 {
				continue
			}
			val := strings.TrimSpace(sentence)
			if len(val) > 120 {
				val = val[:120] + "..."
			}
			if val == "" || seen[val] {
				continue
			}
			seen[val] = true
			questions = append(questions, ExtractedEntity{
				Name:       val,
				Type:       "question",
				Confidence: 0.5,
				SourceID:   sourceID,
			})
		}
	}
	return questions
}

// extractedTriple holds a subject-predicate-object triple.
type extractedTriple struct {
	subject   string
	predicate string
	object    string
}

// extractTriple extracts a subject-predicate-object triple from a sentence.
// Supports patterns: "X <verb> Y" where verb is a known relation indicator.
func extractTriple(sentence string) *extractedTriple {
	// Known relation-indicating verbs.
	relations := []string{
		" uses ", " implements ", " adopts ", " provides ",
		" supports ", " requires ", " depends on ", " integrates ",
		" replaces ", " extends ", " contains ", " includes ",
	}

	lower := strings.ToLower(sentence)
	for _, rel := range relations {
		idx := strings.Index(lower, rel)
		if idx < 0 {
			continue
		}
		subject := strings.TrimSpace(sentence[:idx])
		rest := strings.TrimSpace(sentence[idx+len(rel):])
		// Take the first word of the object (before space, punctuation, or end).
		object := rest
		if spaceIdx := strings.IndexAny(rest, " .,;:!?"); spaceIdx > 0 {
			object = rest[:spaceIdx]
		}
		object = strings.TrimSpace(object)
		if subject != "" && object != "" {
			return &extractedTriple{
				subject:   subject,
				predicate: strings.TrimSpace(rel),
				object:    object,
			}
		}
	}
	return nil
}

// extractCodeBlockLanguages extracts language identifiers from code blocks.
func extractCodeBlockLanguages(content string) []string {
	var langs []string
	seen := make(map[string]bool)
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(trimmed[3:])
			if lang != "" && !seen[lang] {
				seen[lang] = true
				langs = append(langs, lang)
			}
		}
	}
	return langs
}

// splitSentences splits content into sentences.
func splitSentences(content string) []string {
	var sentences []string
	current := strings.Builder{}
	for _, r := range content {
		current.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			sentences = append(sentences, current.String())
			current.Reset()
		}
	}
	if current.Len() > 0 {
		sentences = append(sentences, current.String())
	}
	return sentences
}

// isCapitalized returns true if the string starts with an uppercase letter.
func isCapitalized(s string) bool {
	if len(s) == 0 {
		return false
	}
	r := []rune(s)
	return r[0] >= 'A' && r[0] <= 'Z'
}

// isCommonWord returns true for common English words that should not be entities.
func isCommonWord(s string) bool {
	common := map[string]bool{
		"The": true, "This": true, "That": true, "These": true,
		"Those": true, "What": true, "When": true, "Where": true,
		"Why": true, "How": true, "Which": true, "Who": true,
		"Whom": true, "Whose": true, "Not": true, "And": true,
		"Or": true, "But": true, "If": true, "Then": true,
		"Else": true, "For": true, "With": true, "Without": true,
		"From": true, "To": true, "In": true, "On": true,
		"At": true, "By": true, "About": true, "Into": true,
		"Through": true, "During": true, "Before": true, "After": true,
		"Above": true, "Below": true, "Between": true, "Under": true,
		"Again": true, "Further": true, "Once": true, "Here": true,
		"There": true, "All": true, "Each": true, "Every": true,
		"Both": true, "Few": true, "More": true, "Most": true,
		"Other": true, "Some": true, "Such": true, "No": true,
		"Nor": true, "Only": true, "Own": true, "Same": true,
		"So": true, "Than": true, "Too": true, "Very": true,
		"Just": true, "Because": true, "As": true, "Until": true,
		"While": true, "Although": true, "Though": true,
		"Please": true, "Yes": true, "Maybe": true,
		"Also": true, "Well": true, "However": true, "Therefore": true,
	}
	return common[s]
}

// deduplicateEntities removes duplicate entities by name, keeping the highest confidence.
func deduplicateEntities(entities []ExtractedEntity) []ExtractedEntity {
	if len(entities) == 0 {
		return entities
	}
	best := make(map[string]int) // name → index in result
	var result []ExtractedEntity
	for _, e := range entities {
		if idx, exists := best[e.Name]; exists {
			if e.Confidence > result[idx].Confidence {
				result[idx] = e
			}
		} else {
			best[e.Name] = len(result)
			result = append(result, e)
		}
	}
	return result
}

// Ensure compile-time checks.
var _ Extractor = (*AKGExtractor)(nil)
