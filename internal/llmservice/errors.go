// Package llm provides error definitions for LLM service.
package llmservice

import "errors"

var (
	// ErrInvalidConfig is returned when configuration is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrInvalidLLMConfig is returned when LLM configuration is invalid.
	ErrInvalidLLMConfig = errors.New("invalid LLM configuration")

	// ErrInvalidMessages is returned when messages are empty or invalid.
	ErrInvalidMessages = errors.New("invalid messages")

	// ErrInvalidPrompt is returned when prompt is empty.
	ErrInvalidPrompt = errors.New("invalid prompt")

	// ErrInvalidInput is returned when input is empty.
	ErrInvalidInput = errors.New("invalid input")

	// ErrGenerationFailed is returned when text generation fails.
	ErrGenerationFailed = errors.New("generation failed")

	// ErrEmbeddingFailed is returned when embedding generation fails.
	ErrEmbeddingFailed = errors.New("embedding generation failed")

	// ErrLLMNotAvailable is returned when LLM service is not available.
	ErrLLMNotAvailable = errors.New("LLM service not available")
)
