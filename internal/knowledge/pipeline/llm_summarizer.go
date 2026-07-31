package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// LLMGenerateFunc is the function signature for calling an LLM.
// Implementations can wrap internal/llm.Client or any other LLM provider.
type LLMGenerateFunc func(ctx context.Context, prompt string) (string, error)

// Language constants for LLMSummarizer. Exported so callers can reference
// the canonical values when configuring WithLanguage.
const (
	// LanguageChinese instructs the LLM to produce summaries in Chinese.
	LanguageChinese = "Chinese"
	// LanguageEnglish instructs the LLM to produce summaries in English.
	LanguageEnglish = "English"
)

// DefaultLLMSummaryLanguage is the output language used when no option is
// supplied. It preserves the historical behavior of the original prompt,
// which hardcoded "用中文" / "in Chinese".
const DefaultLLMSummaryLanguage = LanguageChinese

// LLMSummarizerOption configures an LLMSummarizer.
type LLMSummarizerOption func(*LLMSummarizer)

// WithLanguage sets the output language for generated summaries. Pass any
// non-empty string (e.g. LanguageEnglish, LanguageChinese, "Japanese") to
// override the default (DefaultLLMSummaryLanguage). An empty string is
// ignored so the default is retained.
func WithLanguage(lang string) LLMSummarizerOption {
	return func(s *LLMSummarizer) {
		if lang != "" {
			s.language = lang
		}
	}
}

// LLMSummarizer implements knowledge.Summarizer by calling an LLM to generate
// concise, fact-preserving summaries from the Normalized/Raw content.
// Compared to DefaultSummarizer (which just truncates at 200 chars), this
// preserves key technical terms, names, and relationships.
type LLMSummarizer struct {
	generate      LLMGenerateFunc
	maxSummaryLen int    // Target summary length in characters
	language      string // Output language for the LLM prompt
}

// NewLLMSummarizer creates a summarizer that uses an LLM to generate summaries.
// generate is the LLM call function; maxLen controls the target summary length.
// opts configure the summarizer (e.g. WithLanguage). When no language option
// is supplied, DefaultLLMSummaryLanguage is used to preserve historical
// behavior.
func NewLLMSummarizer(generate LLMGenerateFunc, maxLen int, opts ...LLMSummarizerOption) *LLMSummarizer {
	if maxLen <= 0 {
		maxLen = 300
	}
	s := &LLMSummarizer{
		generate:      generate,
		maxSummaryLen: maxLen,
		language:      DefaultLLMSummaryLanguage,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Name returns the summarizer identifier.
func (s *LLMSummarizer) Name() string { return "llm-summarizer" }

// Summarize generates an LLM-based summary for the given KnowledgeObject.
// Falls back to DefaultSummarizer if the LLM call fails.
func (s *LLMSummarizer) Summarize(ctx context.Context, obj *knowledge.KnowledgeObject) (*knowledge.KnowledgeObject, error) {
	if obj == nil {
		return obj, nil
	}

	source := obj.Normalized
	if source == "" {
		source = obj.Summary
	}
	if source == "" && len(obj.Raw) > 0 {
		source = string(obj.Raw)
	}
	if source == "" {
		return obj, nil
	}

	// If source is already short enough and has a summary, skip LLM call.
	if obj.Summary != "" && len(source) <= s.maxSummaryLen {
		return obj, nil
	}

	prompt := s.buildPrompt(source, obj.Type, s.maxSummaryLen)
	summary, err := s.generate(ctx, prompt)
	if err != nil {
		// Fall back to DefaultSummarizer on LLM failure.
		fallback := &DefaultSummarizer{MaxSummaryLen: s.maxSummaryLen}
		return fallback.Summarize(ctx, obj)
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		fallback := &DefaultSummarizer{MaxSummaryLen: s.maxSummaryLen}
		return fallback.Summarize(ctx, obj)
	}

	obj.Summary = summary
	return obj, nil
}

// buildPrompt constructs the LLM prompt for summarization. The output
// language instruction is injected from s.language instead of being
// hardcoded, so non-Chinese deployments can configure a different language.
func (s *LLMSummarizer) buildPrompt(source string, objType knowledge.ObjectType, maxLen int) string {
	var b strings.Builder
	b.WriteString("You are a knowledge summarizer for an AI agent system. ")
	fmt.Fprintf(&b, "Summarize the following technical content in %s. ", s.language)
	b.WriteString("CRITICAL: Preserve ALL of the following in your summary:\n")
	b.WriteString("- All technical terms, proper names, and identifiers\n")
	b.WriteString("- Numbers, versions, and specific values\n")
	b.WriteString("- Architecture names, module names, and their relationships\n")
	b.WriteString("- Key decisions and their rationale\n")
	b.WriteString("- All acronyms and their full forms\n\n")
	fmt.Fprintf(&b, "The content type is: %s\n\n", objType)
	b.WriteString("Content to summarize:\n")
	b.WriteString("--------------------------------------------------\n")
	b.WriteString(source)
	b.WriteString("\n--------------------------------------------------\n\n")
	fmt.Fprintf(&b, "Write a concise summary in %s within %d characters. ", s.language, maxLen)
	b.WriteString("Focus on preserving technical accuracy over brevity.")
	return b.String()
}
