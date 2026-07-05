// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// AgentHandler handles HTTP requests for agent lifecycle management.
type AgentHandler struct {
	agent core.AgentService
}

// NewAgentHandler creates a new agent handler.
// Args:
//
//	agent - the agent service implementation.
//
// Returns:
//
//	handler - the initialized agent handler.
func NewAgentHandler(agent core.AgentService) *AgentHandler {
	return &AgentHandler{agent: agent}
}

// CreateAgentRequest is the request body for POST /api/v1/agents.
type CreateAgentRequest struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name,omitempty"`
	Type   string                 `json:"type,omitempty"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// HandleCreate creates a new agent.
func (h *AgentHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	if h.agent == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "agent service not initialized")
		return
	}

	var req CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.ID == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}

	agent, err := h.agent.CreateAgent(r.Context(), &core.AgentConfig{
		ID:     req.ID,
		Name:   req.Name,
		Type:   req.Type,
		Config: req.Config,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("create agent failed: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, agent)
}

// HandleGet returns an agent by ID.
func (h *AgentHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	if h.agent == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "agent service not initialized")
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		writeJSONError(w, http.StatusBadRequest, "agent id is required")
		return
	}

	agent, err := h.agent.GetAgent(r.Context(), agentID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("agent not found: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, agent)
}

// HandleList returns all agents matching the filter.
func (h *AgentHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if h.agent == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "agent service not initialized")
		return
	}

	filter := &core.AgentFilter{
		Type:      r.URL.Query().Get("type"),
		SessionID: r.URL.Query().Get("session_id"),
	}

	agents, _, err := h.agent.ListAgents(r.Context(), filter)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("list agents: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, agents)
}

// HandleDelete deletes an agent by ID.
func (h *AgentHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	if h.agent == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "agent service not initialized")
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		writeJSONError(w, http.StatusBadRequest, "agent id is required")
		return
	}

	if err := h.agent.DeleteAgent(r.Context(), agentID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("delete agent failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: keyDeleted})
}
