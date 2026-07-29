// Package handler provides HTTP handlers for the ARES API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// LLMHandler handles HTTP requests for LLM inference.
type LLMHandler struct {
	llm core.LLMService
}

// NewLLMHandler creates a new LLM handler.
// Args:
//
//	llm - the LLM service implementation.
//
// Returns:
//
//	handler - the initialized LLM handler.
func NewLLMHandler(llm core.LLMService) *LLMHandler {
	return &LLMHandler{llm: llm}
}

// ChatRequest is the request body for POST /api/v1/llm/chat.
type ChatRequest struct {
	Model    string             `json:"model,omitempty"`
	Messages []*core.LLMMessage `json:"messages"`
	Stream   bool               `json:"stream,omitempty"`
}

// HandleChat handles a chat completion request.
func (h *LLMHandler) HandleChat(w http.ResponseWriter, r *http.Request) {
	if h.llm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "LLM service not initialized")
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if len(req.Messages) == 0 {
		writeJSONError(w, http.StatusBadRequest, "messages is required")
		return
	}

	genReq := &core.GenerateRequest{
		Model:    req.Model,
		Messages: req.Messages,
	}

	resp, err := h.llm.Generate(r.Context(), genReq)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("LLM call failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// HandleGenerateSimple handles a simple text generation request.
func (h *LLMHandler) HandleGenerateSimple(w http.ResponseWriter, r *http.Request) {
	if h.llm == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "LLM service not initialized")
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Prompt == "" {
		writeJSONError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	result, err := h.llm.GenerateSimple(r.Context(), req.Prompt)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("generation failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"content": result})
}
