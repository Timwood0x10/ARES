// Package handler provides HTTP handlers for the ARES API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// WorkflowHandler handles HTTP requests for workflow orchestration.
type WorkflowHandler struct {
	workflow core.WorkflowService
}

// NewWorkflowHandler creates a new workflow handler.
// Args:
//
//	workflow - the workflow service implementation.
//
// Returns:
//
//	handler - the initialized workflow handler.
func NewWorkflowHandler(workflow core.WorkflowService) *WorkflowHandler {
	return &WorkflowHandler{workflow: workflow}
}

// WorkflowExecuteRequest is the request body for POST /api/v1/workflows/execute.
type WorkflowExecuteRequest struct {
	// WorkflowID is the identifier of the workflow to execute.
	WorkflowID string `json:"workflow_id"`
	// Input is the initial input for the workflow.
	Input string `json:"input,omitempty"`
	// Variables overrides workflow-level variables.
	Variables map[string]string `json:"variables,omitempty"`
	// TimeoutSeconds overrides the default execution timeout.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// HandleExecute runs a workflow synchronously and returns the result.
func (h *WorkflowHandler) HandleExecute(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "workflow service not initialized")
		return
	}

	var req WorkflowExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.WorkflowID == "" {
		writeJSONError(w, http.StatusBadRequest, "workflow_id is required")
		return
	}

	coreReq := &core.WorkflowRequest{
		WorkflowID: req.WorkflowID,
		Input:      req.Input,
		Variables:  req.Variables,
	}
	if req.TimeoutSeconds > 0 {
		coreReq.Timeout = time.Duration(req.TimeoutSeconds) * time.Second
	}

	result, err := h.workflow.Execute(r.Context(), coreReq)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("workflow execution failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// HandleList lists all registered workflow definitions.
func (h *WorkflowHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "workflow service not initialized")
		return
	}

	workflows, err := h.workflow.ListWorkflows(r.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("list workflows: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, workflows)
}

// HandleGet returns a workflow definition by ID.
func (h *WorkflowHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	if h.workflow == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "workflow service not initialized")
		return
	}

	workflowID := r.PathValue("id")
	if workflowID == "" {
		writeJSONError(w, http.StatusBadRequest, "workflow id is required")
		return
	}

	workflow, err := h.workflow.GetWorkflow(r.Context(), workflowID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("workflow not found: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, workflow)
}
