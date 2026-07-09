// Package pipeline — LLM-based summarizer for AKF.
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

// LLMSummarizer implements knowledge.Summarizer by calling an LLM to generate
// concise, fact-preserving summaries from the Normalized/Raw content.
// Compared to DefaultSummarizer (which just truncates at 200 chars), this
// preserves key technical terms, names, and relationships.
type LLMSummarizer struct {
	generate      LLMGenerateFunc
	maxSummaryLen int // Target summary length in characters
}

// NewLLMSummarizer creates a summarizer that uses an LLM to generate summaries.
// generate is the LLM call function; maxLen controls the target summary length.
func NewLLMSummarizer(generate LLMGenerateFunc, maxLen int) *LLMSummarizer {
	if maxLen <= 0 {
		maxLen = 300
	}
	return &LLMSummarizer{generate: generate, maxSummaryLen: maxLen}
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

// buildPrompt constructs the LLM prompt for summarization.
func (s *LLMSummarizer) buildPrompt(source string, objType knowledge.ObjectType, maxLen int) string {
	var b strings.Builder
	b.WriteString("You are a knowledge summarizer for an AI agent system. ")
	b.WriteString("Summarize the following technical content in Chinese. ")
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
	fmt.Fprintf(&b, "Write a concise summary in Chinese within %d characters. ", maxLen)
	b.WriteString("Focus on preserving technical accuracy over brevity.")
	return b.String()
}
