// Package knowledge provides the public HTTP API for AKF.
//
// Endpoints:
//
//	POST /kg/build     — build a WorkingGraph from a goal
//	POST /kg/context   — build + compile into LLM-ready formats
//	POST /kg/query     — query knowledge via Intent → Graph → Compile
//	POST /kg/distill   — distill content into KnowledgeObjects
package knowledge

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/retriever"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// Service wraps KnowledgeRuntime + Compiler + Retriever as an HTTP API.
type Service struct {
	rt     *runtime.KnowledgeRuntime
	comp   compiler.Compiler
	ret    *retriever.Retriever
	apiKey string // optional API key for auth
}

const keyError = "error"

// New creates a knowledge API service.
func New(rt *runtime.KnowledgeRuntime, comp compiler.Compiler, ret *retriever.Retriever) *Service {
	return &Service{
		rt:   rt,
		comp: comp,
		ret:  ret,
	}
}

// SetAPIKey enables API key authentication on all routes.
func (s *Service) SetAPIKey(key string) { s.apiKey = key }

// RegisterRoutes attaches AKF endpoints to a Gin router group.
// All routes are mounted under /kg.
func (s *Service) RegisterRoutes(rg *gin.RouterGroup) {
	kg := rg.Group("/kg")
	if s.apiKey != "" {
		kg.Use(s.authMiddleware())
	}
	kg.POST("/build", s.handleBuild)
	kg.POST("/context", s.handleContext)
	kg.POST("/query", s.handleQuery)
	kg.POST("/distill", s.handleDistill)
}

func (s *Service) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetHeader("Authorization") != "Bearer "+s.apiKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{keyError: "invalid or missing API key"})
			return
		}
		c.Next()
	}
}

// ── request / response types ──

type buildRequest struct {
	Goal      string `json:"goal" binding:"required"`
	MaxTokens int    `json:"max_tokens"`
	ForGraph  int    `json:"for_graph"`
}

type contextRequest struct {
	Goal      string   `json:"goal" binding:"required"`
	Formats   []string `json:"formats"`
	MaxTokens int      `json:"max_tokens"`
	ForGraph  int      `json:"for_graph"`
}

type queryRequest struct {
	Text       string   `json:"text" binding:"required"`
	Types      []string `json:"types"`
	MaxResults int      `json:"max_results"`
	MaxTokens  int      `json:"max_tokens"`
	Formats    []string `json:"formats"`
}

type distillRequest struct {
	Content string   `json:"content" binding:"required"`
	Tags    []string `json:"tags"`
	Type    string   `json:"type"`
}

// ── handlers ──

func (s *Service) handleBuild(c *gin.Context) {
	var req buildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{keyError: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	budget := knowledge.TokenBudget{
		MaxTokens: req.MaxTokens,
		ForGraph:  req.ForGraph,
	}
	if budget.MaxTokens <= 0 {
		budget = knowledge.TokenBudget{MaxTokens: 5000, ForGraph: 3000, Reserved: 2000}
	}
	if budget.ForGraph <= 0 {
		budget.ForGraph = budget.MaxTokens * 60 / 100
	}
	budget.Reserved = budget.MaxTokens - budget.ForGraph
	if budget.Reserved < 0 {
		budget.Reserved = 0
	}

	graph, err := s.rt.Execute(c.Request.Context(), req.Goal, budget, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{keyError: fmt.Sprintf("build: %v", err)})
		return
	}

	nodeSummaries := make([]map[string]any, 0, len(graph.Nodes))
	for id, obj := range graph.Nodes {
		nodeSummaries = append(nodeSummaries, map[string]any{
			"id":         id,
			"type":       obj.Type,
			"summary":    obj.Summary,
			"confidence": obj.Confidence,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"nodes":      nodeSummaries,
		"edges":      graph.Edges,
		"node_count": len(graph.Nodes),
		"edge_count": len(graph.Edges),
	})
}

func (s *Service) handleContext(c *gin.Context) {
	var req contextRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{keyError: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	budget := knowledge.TokenBudget{
		MaxTokens: req.MaxTokens,
		ForGraph:  req.ForGraph,
	}
	if budget.MaxTokens <= 0 {
		budget = knowledge.TokenBudget{MaxTokens: 5000, ForGraph: 3000, Reserved: 2000}
	}
	if budget.ForGraph <= 0 {
		budget.ForGraph = budget.MaxTokens * 60 / 100
	}
	budget.Reserved = budget.MaxTokens - budget.ForGraph
	if budget.Reserved < 0 {
		budget.Reserved = 0
	}

	graph, err := s.rt.Execute(c.Request.Context(), req.Goal, budget, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{keyError: fmt.Sprintf("build: %v", err)})
		return
	}

	formats := make([]compiler.Format, len(req.Formats))
	for i, f := range req.Formats {
		formats[i] = compiler.Format(f)
	}
	if len(formats) == 0 {
		formats = []compiler.Format{compiler.FormatPrompt}
	}

	compiled, err := s.comp.Compile(c.Request.Context(), graph, compiler.CompileConfig{Formats: formats})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{keyError: fmt.Sprintf("compile: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"formats":       compiled.Formats,
		"input_nodes":   compiled.Metrics.InputNodes,
		"output_tokens": compiled.Metrics.OutputTokens,
	})
}

func (s *Service) handleQuery(c *gin.Context) {
	var req queryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{keyError: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	types := make([]knowledge.ObjectType, len(req.Types))
	for i, t := range req.Types {
		types[i] = knowledge.ObjectType(t)
	}

	formats := make([]compiler.Format, len(req.Formats))
	for i, f := range req.Formats {
		formats[i] = compiler.Format(f)
	}

	result, err := s.ret.Retrieve(c.Request.Context(), retriever.Query{
		Text:       req.Text,
		Types:      types,
		MaxResults: req.MaxResults,
		MaxTokens:  req.MaxTokens,
		Formats:    formats,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{keyError: fmt.Sprintf("query: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"context":    result.Context,
		"node_ids":   nodeIDs(result.Graph),
		"node_count": len(result.Graph.Nodes),
	})
}

func (s *Service) handleDistill(c *gin.Context) {
	var req distillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{keyError: fmt.Sprintf("invalid request: %v", err)})
		return
	}

	// Simple direct distillation without a full Distiller.
	obj := &knowledge.KnowledgeObject{
		ID:         fmt.Sprintf("distill_%d", time.Now().UnixNano()),
		Type:       knowledge.ObjectType(req.Type),
		Summary:    req.Content,
		Normalized: req.Content,
		Tags:       req.Tags,
		Confidence: 1.0,
	}

	if req.Type == "" {
		obj.Type = knowledge.ObjectMemory
	}

	c.JSON(http.StatusOK, gin.H{
		"object_id": obj.ID,
		"type":      obj.Type,
		"summary":   obj.Summary,
	})
}

// ── helpers ──

func nodeIDs(g *knowledge.WorkingGraph) []string {
	ids := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	return ids
}
