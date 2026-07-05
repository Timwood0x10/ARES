// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// MemoryHandler handles HTTP requests for memory and session management.
type MemoryHandler struct {
	memory core.MemoryService
}

// NewMemoryHandler creates a new memory handler.
// Args:
//
//	memory - the memory service implementation.
//
// Returns:
//
//	handler - the initialized memory handler.
func NewMemoryHandler(memory core.MemoryService) *MemoryHandler {
	return &MemoryHandler{memory: memory}
}

// CreateSessionRequest is the request body for POST /api/v1/sessions.
type CreateSessionRequest struct {
	UserID   string `json:"user_id"`
	TenantID string `json:"tenant_id,omitempty"`
}

// AddMessageRequest is the request body for POST /api/v1/sessions/{id}/messages.
type AddMessageRequest struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// HandleCreateSession creates a new session.
func (h *MemoryHandler) HandleCreateSession(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "memory service not initialized")
		return
	}

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.UserID == "" {
		writeJSONError(w, http.StatusBadRequest, "user_id is required")
		return
	}

	sessionID, err := h.memory.CreateSession(r.Context(), &core.SessionConfig{
		UserID:   req.UserID,
		TenantID: req.TenantID,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("create session failed: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"session_id": sessionID})
}

// HandleGetSession returns a session by ID.
func (h *MemoryHandler) HandleGetSession(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "memory service not initialized")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "session id is required")
		return
	}

	session, err := h.memory.GetSession(r.Context(), sessionID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("session not found: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, session)
}

// HandleDeleteSession deletes a session and all its messages.
func (h *MemoryHandler) HandleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "memory service not initialized")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "session id is required")
		return
	}

	if err := h.memory.DeleteSession(r.Context(), sessionID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("delete session failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: keyDeleted})
}

// HandleAddMessage adds a message to a session.
func (h *MemoryHandler) HandleAddMessage(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "memory service not initialized")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "session id is required")
		return
	}

	var req AddMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.Role == "" || req.Content == "" {
		writeJSONError(w, http.StatusBadRequest, "role and content are required")
		return
	}

	if err := h.memory.AddMessage(r.Context(), sessionID, core.MessageRole(req.Role), req.Content); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("add message failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: "added"})
}

// HandleGetMessages returns messages from a session.
func (h *MemoryHandler) HandleGetMessages(w http.ResponseWriter, r *http.Request) {
	if h.memory == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "memory service not initialized")
		return
	}

	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeJSONError(w, http.StatusBadRequest, "session id is required")
		return
	}

	messages, err := h.memory.GetMessages(r.Context(), sessionID, nil)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("get messages failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, messages)
}
