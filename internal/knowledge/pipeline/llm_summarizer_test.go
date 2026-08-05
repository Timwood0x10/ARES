package pipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// captureGenerate returns an LLMGenerateFunc that records the prompt it
// receives and replies with a fixed summary.
func captureGenerate(captured *string, reply string) LLMGenerateFunc {
	return func(_ context.Context, prompt string) (string, error) {
		*captured = prompt
		return reply, nil
	}
}

// TestLLMSummarizer_DefaultLanguageIsChinese verifies that the summarizer
// defaults to Chinese when no WithLanguage option is supplied. This locks
// in backward compatibility with the original hardcoded prompt.
func TestLLMSummarizer_DefaultLanguageIsChinese(t *testing.T) {
	var prompt string
	s := NewLLMSummarizer(captureGenerate(&prompt, "ok"), 100)

	if _, err := s.Summarize(context.Background(), &knowledge.KnowledgeObject{
		ID:         "obj1",
		Normalized: "some technical content about Redis caching",
	}); err != nil {
		t.Fatalf("Summarize error: %v", err)
	}

	if !strings.Contains(prompt, "in Chinese") {
		t.Errorf("default prompt should instruct Chinese output; prompt=%q", prompt)
	}
	if strings.Contains(prompt, "in English") {
		t.Errorf("default prompt must not mention English; prompt=%q", prompt)
	}
}

// TestLLMSummarizer_WithLanguageEnglish verifies that WithLanguage injects
// the configured language into the prompt instead of the hardcoded Chinese.
func TestLLMSummarizer_WithLanguageEnglish(t *testing.T) {
	var prompt string
	s := NewLLMSummarizer(
		captureGenerate(&prompt, "ok"),
		100,
		WithLanguage(LanguageEnglish),
	)

	if _, err := s.Summarize(context.Background(), &knowledge.KnowledgeObject{
		ID:         "obj2",
		Normalized: "some technical content about PostgreSQL persistence",
	}); err != nil {
		t.Fatalf("Summarize error: %v", err)
	}

	if !strings.Contains(prompt, "in English") {
		t.Errorf("prompt should instruct English output; prompt=%q", prompt)
	}
	if strings.Contains(prompt, "in Chinese") {
		t.Errorf("prompt must not mention Chinese when English configured; prompt=%q", prompt)
	}
}

// TestLLMSummarizer_WithLanguageEmptyIgnored verifies that an empty
// language string is ignored and the default is retained.
func TestLLMSummarizer_WithLanguageEmptyIgnored(t *testing.T) {
	var prompt string
	s := NewLLMSummarizer(
		captureGenerate(&prompt, "ok"),
		100,
		WithLanguage(""),
	)

	if _, err := s.Summarize(context.Background(), &knowledge.KnowledgeObject{
		ID:         "obj3",
		Normalized: "content",
	}); err != nil {
		t.Fatalf("Summarize error: %v", err)
	}

	if !strings.Contains(prompt, "in Chinese") {
		t.Errorf("empty language should fall back to Chinese default; prompt=%q", prompt)
	}
}

// TestLLMSummarizer_WithLanguageCustomValue verifies that an arbitrary
// language string (not in the constants) is injected verbatim.
func TestLLMSummarizer_WithLanguageCustomValue(t *testing.T) {
	var prompt string
	s := NewLLMSummarizer(
		captureGenerate(&prompt, "ok"),
		100,
		WithLanguage("Japanese"),
	)

	if _, err := s.Summarize(context.Background(), &knowledge.KnowledgeObject{
		ID:         "obj4",
		Normalized: "content",
	}); err != nil {
		t.Fatalf("Summarize error: %v", err)
	}

	if !strings.Contains(prompt, "in Japanese") {
		t.Errorf("prompt should instruct Japanese output; prompt=%q", prompt)
	}
}

// TestLLMSummarizer_Name verifies the summarizer identifier.
func TestLLMSummarizer_Name(t *testing.T) {
	s := NewLLMSummarizer(captureGenerate(new(string), "ok"), 100)
	if s.Name() != "llm-summarizer" {
		t.Errorf("expected llm-summarizer, got %s", s.Name())
	}
}
