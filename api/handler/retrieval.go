// Package handler provides HTTP handlers for the GoAgent API.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Timwood0x10/ares/api/core"
)

// RetrievalHandler handles HTTP requests for knowledge base retrieval.
type RetrievalHandler struct {
	retrieval core.RetrievalService
}

// NewRetrievalHandler creates a new retrieval handler.
// Args:
//
//	retrieval - the retrieval service implementation.
//
// Returns:
//
//	handler - the initialized retrieval handler.
func NewRetrievalHandler(retrieval core.RetrievalService) *RetrievalHandler {
	return &RetrievalHandler{retrieval: retrieval}
}

// SearchRequest is the request body for POST /api/v1/knowledge/search.
type SearchRequest struct {
	TenantID string `json:"tenant_id"`
	Query    string `json:"query"`
}

// AddKnowledgeRequest is the request body for POST /api/v1/knowledge.
type AddKnowledgeRequest struct {
	TenantID string `json:"tenant_id"`
	Content  string `json:"content"`
	Source   string `json:"source,omitempty"`
	Metadata string `json:"metadata,omitempty"`
}

// HandleSearch performs a knowledge base search.
func (h *RetrievalHandler) HandleSearch(w http.ResponseWriter, r *http.Request) {
	if h.retrieval == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "retrieval service not initialized")
		return
	}

	var req SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.TenantID == "" || req.Query == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant_id and query are required")
		return
	}

	results, err := h.retrieval.Search(r.Context(), req.TenantID, req.Query)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("search failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, results)
}

// HandleAddKnowledge adds a new knowledge item.
func (h *RetrievalHandler) HandleAddKnowledge(w http.ResponseWriter, r *http.Request) {
	if h.retrieval == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "retrieval service not initialized")
		return
	}

	var req AddKnowledgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}
	if req.TenantID == "" || req.Content == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant_id and content are required")
		return
	}

	item := &core.KnowledgeItem{
		TenantID: req.TenantID,
		Content:  req.Content,
		Source:   req.Source,
	}

	created, err := h.retrieval.AddKnowledge(r.Context(), item)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("add knowledge failed: %v", err))
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// HandleGetKnowledge returns a knowledge item by ID.
func (h *RetrievalHandler) HandleGetKnowledge(w http.ResponseWriter, r *http.Request) {
	if h.retrieval == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "retrieval service not initialized")
		return
	}

	tenantID := r.PathValue("tenant_id")
	itemID := r.PathValue("id")
	if tenantID == "" || itemID == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant_id and id are required")
		return
	}

	item, err := h.retrieval.GetKnowledge(r.Context(), tenantID, itemID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("knowledge not found: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, item)
}

// HandleDeleteKnowledge deletes a knowledge item.
func (h *RetrievalHandler) HandleDeleteKnowledge(w http.ResponseWriter, r *http.Request) {
	if h.retrieval == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "retrieval service not initialized")
		return
	}

	tenantID := r.PathValue("tenant_id")
	itemID := r.PathValue("id")
	if tenantID == "" || itemID == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant_id and id are required")
		return
	}

	if err := h.retrieval.DeleteKnowledge(r.Context(), tenantID, itemID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("delete knowledge failed: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{keyStatus: keyDeleted})
}
