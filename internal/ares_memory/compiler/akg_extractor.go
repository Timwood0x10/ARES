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

// Confidence tiers (Phase 1 L2 semanticization). These replace the previously
// scattered rule-hit literals so that AKGMinConfidence (raised to 0.6) filters
// low-signal extractions instead of passing nearly everything. The tier a node
// lands in reflects the STRENGTH OF THE SIGNAL that produced it, not whether a
// rule fired:
//   - confStrong: deliberate human statements (decisions, constraints,
//     tradeoffs) and explicit spans (quoted terms, code-block languages).
//   - confMedium: structured extractions (fact triples) and curated terms
//     (Chinese dictionary / capitalized proper nouns).
//   - confWeak: heuristic guesses (Chinese noun-phrase suffix runs, open
//     questions) that are cheap to extract but often noisy.
const (
	confStrong = 0.9
	confMedium = 0.7
	confWeak   = 0.4
)

// extraChineseTerms extends the alias-table Chinese terms with vocabulary
// specific to the knowledge-graph / distillation / compression domain. It is
// declared before chineseTermDict because that variable's initializer reads
// it during package initialization (Go does not infer dependencies through
// function calls, so declaration order matters).
var extraChineseTerms = []string{
	"知识图谱", "记忆蒸馏", "对话压缩", "进化系统", "智能体", "编译器",
	"蒸馏器", "检索", "进化", "压缩", "编译", "管线", "流水线",
	"适配器", "协调器", "调度器", "恢复器", "向量检索", "实体识别",
	"实体抽取", "关系抽取", "提示词", "上下文", "上下文窗口",
	"多智能体", "子智能体", "知识库", "蒸馏方案", "中文NER",
}

// chineseNounSuffixes mark a preceding CJK run as a noun-phrase entity.
var chineseNounSuffixes = []string{
	"模块", "系统", "组件", "服务", "框架", "协议", "算法", "引擎",
	"接口", "配置", "流程", "层", "器", "库", "表", "链", "池", "方案", "模型",
}

// chineseTermDict seeds Chinese entity extraction with project-relevant
// technical terms. It reuses the Chinese aliases already defined in the
// RuleNormalizer alias table (see normalizer.go) and extends them with
// additional graph/distillation vocabulary so that extraction stays aligned
// with the canonical names used elsewhere in the compiler.
var chineseTermDict = buildChineseTermDict()

// AKGExtractor implements the Extractor interface using AKG (Knowledge Fabric)
// infrastructure. It extracts entities and facts from conversation messages
// using rule-based parsing and NER, with zero LLM token cost.
//
// The extractor supports both English and Chinese input: English uses
// capitalized-term and pattern heuristics, while Chinese uses a term
// dictionary (seeded from the alias table), quoted-span detection, and
// noun-suffix heuristics.
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
//  3. Extract entities via pattern matching (code blocks, references, keywords,
//     and Chinese term/quoted/noun heuristics).
//  4. Extract facts via structured triple extraction (English and Chinese).
//  5. Extract decisions, constraints, tradeoffs, and open questions.
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
					Confidence: confStrong,
					SourceID:   sourceID,
				})
			}
		}
	}
	return entities
}

// extractEntities extracts entities from a normalized message.
// Uses rule-based patterns: code blocks, capitalized terms (English), and
// Chinese term/quoted/noun-phrase heuristics. The seen set is shared across
// the English and Chinese passes to avoid duplicate entity names.
func (e *AKGExtractor) extractEntities(normalized *knowledge.KnowledgeObject, sourceID string) []ExtractedEntity {
	content := normalized.Normalized
	if content == "" {
		return nil
	}

	var entities []ExtractedEntity
	seen := make(map[string]bool)

	// English path: code-block languages and capitalized terms.
	codeBlockLangs := extractCodeBlockLanguages(content)
	for _, lang := range codeBlockLangs {
		if !seen[lang] {
			seen[lang] = true
			entities = append(entities, ExtractedEntity{
				Name:       lang,
				Type:       entityTypeLanguage,
				Confidence: confStrong,
				SourceID:   sourceID,
			})
		}
	}

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
					Confidence: confMedium,
					SourceID:   sourceID,
				})
			}
		}
	}

	// Chinese path: dictionary terms, quoted spans, and noun-phrase suffixes.
	entities = append(entities, extractChineseTermEntities(content, sourceID, seen)...)
	entities = append(entities, extractQuotedTermEntities(content, sourceID, seen)...)

	return entities
}

// extractChineseTermEntities extracts Chinese entities from content using the
// shared term dictionary (seeded from the alias table and project vocabulary)
// plus a noun-phrase suffix heuristic for CJK runs ending in known suffixes.
// The seen set is shared with the caller to avoid cross-pass duplicates.
func extractChineseTermEntities(content, sourceID string, seen map[string]bool) []ExtractedEntity {
	var entities []ExtractedEntity

	// Dictionary match: any known Chinese technical term present in content.
	for _, term := range chineseTermDict {
		if seen[term] || !strings.Contains(content, term) {
			continue
		}
		seen[term] = true
		entities = append(entities, ExtractedEntity{
			Name:       term,
			Type:       entityTypeConcept,
			Confidence: confMedium,
			SourceID:   sourceID,
		})
	}

	// Noun-phrase heuristic: CJK runs ending in a known noun suffix.
	for _, np := range extractChineseNounPhrases(content) {
		if seen[np] {
			continue
		}
		seen[np] = true
		entities = append(entities, ExtractedEntity{
			Name:       np,
			Type:       entityTypeConcept,
			Confidence: confWeak,
			SourceID:   sourceID,
		})
	}

	return entities
}

// extractChineseNounPhrases returns maximal CJK runs in content that end with a
// known noun suffix (e.g., "进化系统", "对话压缩模块"). Runs shorter than two
// characters or without a suffix are ignored to reduce noise.
func extractChineseNounPhrases(content string) []string {
	var phrases []string
	seen := make(map[string]bool)
	runes := []rune(content)
	n := len(runes)
	i := 0
	for i < n {
		if !isCJK(runes[i]) {
			i++
			continue
		}
		start := i
		for i < n && isCJK(runes[i]) {
			i++
		}
		run := string(runes[start:i])
		if len([]rune(run)) < 2 || seen[run] {
			continue
		}
		if endsWithSuffix(run, chineseNounSuffixes) {
			seen[run] = true
			phrases = append(phrases, run)
		}
	}
	return phrases
}

// extractQuotedTermEntities extracts Chinese-containing spans enclosed in
// paired quotes ("…", "…", 「…」) or inline backticks. Quoted spans are
// treated as high-confidence entities because quoting signals an intentional
// term or concept reference. The seen set is shared with the caller.
func extractQuotedTermEntities(content, sourceID string, seen map[string]bool) []ExtractedEntity {
	var entities []ExtractedEntity
	runes := []rune(content)
	n := len(runes)

	// Paired quote types: ASCII double, curly double, corner brackets.
	for i := 0; i < n; i++ {
		var closeRune rune
		switch runes[i] {
		case '"':
			closeRune = '"'
		case '“':
			closeRune = '”'
		case '「':
			closeRune = '」'
		default:
			continue
		}
		j := i + 1
		for j < n && runes[j] != closeRune {
			j++
		}
		if j >= n {
			continue
		}
		addQuotedEntity(string(runes[i+1:j]), sourceID, seen, &entities)
		i = j
	}

	// Inline backtick code spans (skip triple-fence markers handled elsewhere).
	for i := 0; i < n; i++ {
		if runes[i] != '`' {
			continue
		}
		if i+2 < n && runes[i+1] == '`' && runes[i+2] == '`' {
			i += 2 // skip remaining two backticks of the triple-fence marker
			continue
		}
		j := i + 1
		for j < n && runes[j] != '`' {
			j++
		}
		if j >= n {
			continue
		}
		addQuotedEntity(string(runes[i+1:j]), sourceID, seen, &entities)
		i = j
	}

	return entities
}

// addQuotedEntity appends a quoted span as an entity when it contains CJK text
// and has not been seen before.
func addQuotedEntity(span, sourceID string, seen map[string]bool, entities *[]ExtractedEntity) {
	term := strings.TrimSpace(span)
	if term == "" || !hasCJK(term) || seen[term] {
		return
	}
	seen[term] = true
	*entities = append(*entities, ExtractedEntity{
		Name:       term,
		Type:       entityTypeConcept,
		Confidence: confStrong,
		SourceID:   sourceID,
	})
}

// extractFacts extracts structured triples from a normalized message.
// Uses rule-based patterns for both English ("X uses Y") and Chinese
// ("X实现了Y"). Object phrases are kept whole up to the next clause boundary.
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
				Confidence: confMedium,
				SourceID:   sourceID,
			})
		}
	}
	return facts
}

// extractDecisions extracts decision nodes from normalized content.
// English patterns: "we chose X", "we decided to Y". Chinese patterns:
// "我们决定", "我们选择了", "采用", etc. The extracted value is trimmed to the
// containing clause so decisions do not bleed into unrelated text.
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
		// Chinese decision markers (no surrounding spaces in CJK text).
		{"我们决定", attrChoice},
		{"我们决定采用", attrChoice},
		{"我们选择了", attrChoice},
		{"我们选择", attrChoice},
		{"决定采用", attrChoice},
		{"决定使用", attrChoice},
		{"选用", attrChoice},
		{"选定", attrChoice},
		{"否决了", attrRejection},
		{"放弃了", attrRejection},
		{"废弃了", attrRejection},
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, dp := range decisionPatterns {
			idx := strings.Index(lower, dp.prefix)
			if idx < 0 {
				continue
			}
			val := trimToClauseBoundary(sentence[idx+len(dp.prefix):])
			if val == "" || seen[val] {
				continue
			}
			seen[val] = true
			decisions = append(decisions, ExtractedEntity{
				Name:       val,
				Type:       "decision_" + dp.field,
				Confidence: confStrong,
				SourceID:   sourceID,
			})
		}
	}
	return decisions
}

// extractConstraints extracts constraint nodes from normalized content.
// English patterns: "must be", "cannot". Chinese patterns: "必须", "不能",
// "需要", "禁止", etc.
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
		// Chinese constraint markers.
		"必须", "不能", "需要", "要求", "禁止", "务必", "只允许", "不允许", "应当", "应该",
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, indicator := range constraintIndicators {
			idx := strings.Index(lower, indicator)
			if idx < 0 {
				continue
			}
			val := trimToClauseBoundary(sentence)
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
				Confidence: confStrong,
				SourceID:   sourceID,
			})
		}
	}
	return constraints
}

// extractTradeoffs extracts tradeoff nodes from normalized content.
// English patterns: "tradeoff between X and Y", "at the cost of". Chinese
// patterns: "不过", "然而", "代价是", "权衡", etc.
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
		// Chinese tradeoff markers.
		"不过", "然而", "但是", "代价是", "代价为", "牺牲了", "权衡", "优点是", "缺点是", "尽管如此",
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, indicator := range tradeoffIndicators {
			idx := strings.Index(lower, indicator)
			if idx < 0 {
				continue
			}
			val := trimToClauseBoundary(sentence)
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
				Confidence: confStrong,
				SourceID:   sourceID,
			})
		}
	}
	return tradeoffs
}

// extractOpenQuestions extracts open question nodes from normalized content.
// English patterns: "we need to figure out", "TODO". Chinese patterns:
// "待确认", "待定", "需要调研", etc.
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
		// Sentence-start English variants (no leading space).
		"we need to figure out ", "we need to determine ",
		"we should investigate ", "we should explore ",
		"not yet decided ", "not yet resolved ",
		// Chinese open-question markers.
		"待确认", "待定", "待解决", "待讨论", "尚未决定", "未决定",
		"需调研", "需要调研", "需要确认", "待评审", "待办", "调研一下",
	}

	for _, sentence := range sentences {
		lower := strings.ToLower(sentence)
		for _, indicator := range questionIndicators {
			idx := strings.Index(lower, indicator)
			if idx < 0 {
				continue
			}
			val := trimToClauseBoundary(sentence)
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
				Confidence: confWeak,
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
// Supports English ("X <verb> Y") and Chinese ("X<动词>Y") relation patterns.
// The object is kept whole up to the next clause boundary so multi-word
// objects (e.g., "Patch for runtime updates") are preserved instead of being
// truncated to the first token.
func extractTriple(sentence string) *extractedTriple {
	relations := []string{
		" uses ", " implements ", " adopts ", " provides ",
		" supports ", " requires ", " depends on ", " integrates ",
		" replaces ", " extends ", " contains ", " includes ",
		// Chinese relation verbs (matched without surrounding spaces because
		// CJK text typically omits inter-token whitespace).
		"实现了", "采用", "依赖", "替换", "包含", "提供", "替代", "集成",
	}

	lower := strings.ToLower(sentence)
	for _, rel := range relations {
		idx := strings.Index(lower, rel)
		if idx < 0 {
			continue
		}
		subject := strings.TrimSpace(sentence[:idx])
		rest := strings.TrimSpace(sentence[idx+len(rel):])
		object := trimToClauseBoundary(rest)
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

// splitSentences splits content into sentences using both ASCII and CJK
// terminators. It avoids splitting on decimal points (3.14), version numbers
// (v0.2.7), and dotted abbreviations (e.g., U.S.A.), so numeric and abbreviated
// content is preserved intact.
func splitSentences(content string) []string {
	var sentences []string
	current := strings.Builder{}
	runes := []rune(content)
	n := len(runes)
	for i := 0; i < n; i++ {
		r := runes[i]
		current.WriteRune(r)
		if !isSentenceTerminator(r) {
			continue
		}
		// A '.' that precedes a digit is a decimal/version separator.
		if r == '.' && i+1 < n && isASCIIDigit(runes[i+1]) {
			continue
		}
		// A '.' that is part of a dotted abbreviation (e.g. "e.g.", "U.S.A.")
		// is skipped: it is preceded by a letter and either followed by
		// "letter." or itself preceded by another dot of the same abbreviation.
		if r == '.' && i > 0 && isASCIILetter(runes[i-1]) {
			if (i+1 < n && isASCIILetter(runes[i+1])) || (i-2 >= 0 && runes[i-2] == '.') {
				continue
			}
		}
		s := strings.TrimSpace(current.String())
		if s != "" {
			sentences = append(sentences, s)
		}
		current.Reset()
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		sentences = append(sentences, s)
	}
	return sentences
}

// isSentenceTerminator reports whether r ends a sentence in either ASCII or
// CJK punctuation.
func isSentenceTerminator(r rune) bool {
	switch r {
	case '.', '!', '?', '。', '！', '？', '；', '…':
		return true
	}
	return false
}

// trimToClauseBoundary truncates s at the first clause/punctuation boundary so
// extracted values do not run past the current clause into unrelated text.
// Both ASCII and CJK boundary punctuation are recognized.
func trimToClauseBoundary(s string) string {
	if idx := strings.IndexAny(s, "。！？；，。,.!?;:"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// isCapitalized returns true if the string starts with an uppercase letter.
func isCapitalized(s string) bool {
	if len(s) == 0 {
		return false
	}
	r := []rune(s)
	return r[0] >= 'A' && r[0] <= 'Z'
}

// isASCIIDigit reports whether r is an ASCII digit (0-9).
func isASCIIDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

// isASCIILetter reports whether r is an ASCII letter (a-z or A-Z).
func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isCJK reports whether r is a CJK unified ideograph.
func isCJK(r rune) bool {
	return r >= 0x4E00 && r <= 0x9FFF
}

// hasCJK reports whether s contains at least one CJK unified ideograph.
func hasCJK(s string) bool {
	for _, r := range s {
		if isCJK(r) {
			return true
		}
	}
	return false
}

// endsWithSuffix reports whether s ends with any of the given suffixes.
func endsWithSuffix(s string, suffixes []string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

// buildChineseTermDict seeds the Chinese entity dictionary from the
// RuleNormalizer alias table (all CJK keys) plus the project-specific terms in
// extraChineseTerms. Terms are de-duplicated and order is not significant.
func buildChineseTermDict() []string {
	seen := make(map[string]bool)
	var terms []string
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		terms = append(terms, t)
	}
	for k := range defaultAliasTable() {
		if hasCJK(k) {
			add(k)
		}
	}
	for _, t := range extraChineseTerms {
		add(t)
	}
	return terms
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
