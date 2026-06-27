// Package dashboard - agent orchestration for the web dashboard.
package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
	"github.com/Timwood0x10/ares/internal/llm/output"
)

// AgentTemplate defines a reusable agent configuration.
type AgentTemplate struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	MCPTool     string         `json:"mcp_tool"`
	MCPArgs     map[string]any `json:"mcp_args,omitempty"`
	LLMPrompt   string         `json:"llm_prompt"`
}

// AgentStep defines a single tool call in a multi-step agent.
type AgentStep struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args,omitempty"`
}

// AgentRequest holds a request to create and run an agent.
type AgentRequest struct {
	TemplateID string         `json:"template_id,omitempty"` // Use a template
	Name       string         `json:"name,omitempty"`        // Or custom name
	MCPTool    string         `json:"mcp_tool,omitempty"`    // Or custom MCP tool
	MCPArgs    map[string]any `json:"mcp_args,omitempty"`
	LLMPrompt  string         `json:"llm_prompt,omitempty"`  // Or custom prompt
	Target     string         `json:"target,omitempty"`      // Target to analyze
	Steps      []AgentStep    `json:"steps,omitempty"`       // Multi-step tool calls
	ResumeFrom string         `json:"resume_from,omitempty"` // Agent ID to resume from after kill
}

// AgentResult holds the full result of an agent run.
type AgentResult struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	Progress        int       `json:"progress"`
	MCPTool         string    `json:"mcp_tool"`
	RawDataLen      int       `json:"raw_data_len"`
	Analysis        string    `json:"analysis"`
	Error           string    `json:"error,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	Duration        string    `json:"duration,omitempty"`
	ResurrectionCnt int       `json:"resurrection_cnt"`
}

// MCPExecutor abstracts MCP tool calls for the orchestrator.
type MCPExecutor interface {
	CallTool(ctx context.Context, name string, args map[string]any) (*MCPToolResult, error)
	ListTools(ctx context.Context) ([]MCPToolInfo, error)
}

// MCPToolResult is a simplified tool call result.
type MCPToolResult struct {
	Content []MCPContentBlock `json:"content"`
	IsError bool              `json:"is_error"`
}

// MCPContentBlock is a simplified content block.
type MCPContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// MCPToolInfo is a simplified tool info.
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// LLMExecutor abstracts LLM calls for the orchestrator.
type LLMExecutor interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

// StreamLLMExecutor is an optional extension of LLMExecutor that supports streaming.
// Implement this interface on your LLMExecutor to enable real-time chunk broadcasting.
type StreamLLMExecutor interface {
	GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error)
}

// StreamChunk represents a single chunk in a streaming LLM response.
type StreamChunk struct {
	Content string // Text content of this chunk. May be empty for final chunk.
	Done    bool   // True when this is the final chunk.
	Err     error  // Non-nil only on final chunk if an error occurred.
}

// Orchestrator manages agent lifecycle — creation, execution, results.
type Orchestrator struct {
	mcp         MCPExecutor
	llm         LLMExecutor
	templates   []AgentTemplate
	toolAliases map[string]string // short-name → full-name mappings for MCP tools
	agents      map[string]*AgentResult
	cancels     map[string]context.CancelFunc // per-agent cancel functions
	hub         *WSHub                        // optional, for real-time WS updates
	store       *ares_events.MemoryEventStore // optional, for event persistence
	flight      *flight.FlightRecorder        // optional, for flight recording
	mu          sync.RWMutex
	nextID      atomic.Int64
	agentWg     sync.WaitGroup // tracks background agent goroutines
	baseCtx     context.Context
	baseCancel  context.CancelFunc
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(mcp MCPExecutor, llm LLMExecutor) *Orchestrator {
	ctx, cancel := context.WithCancel(context.Background())
	return &Orchestrator{
		mcp:        mcp,
		llm:        llm,
		agents:     make(map[string]*AgentResult),
		cancels:    make(map[string]context.CancelFunc),
		baseCtx:    ctx,
		baseCancel: cancel,
	}
}

// SetHub attaches a WebSocket hub for real-time agent updates.
func (o *Orchestrator) SetHub(hub *WSHub) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.hub = hub
}

// SetEventStore attaches an event store for event persistence.
func (o *Orchestrator) SetEventStore(store *ares_events.MemoryEventStore) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.store = store
}

// SetFlightRecorder attaches a FlightRecorder for runtime flight data recording.
func (o *Orchestrator) SetFlightRecorder(fr *flight.FlightRecorder) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flight = fr
}

// Stop cancels the base context, signalling all running agents to shut down,
// and waits for every background agent goroutine to finish.
func (o *Orchestrator) Stop() {
	o.baseCancel()
	o.agentWg.Wait()
}

// EventStore returns the current event store. May be nil if not configured.
func (o *Orchestrator) EventStore() *ares_events.MemoryEventStore {
	return o.getStore()
}

// getFlight returns the current FlightRecorder under a read lock. Safe for concurrent use.
func (o *Orchestrator) getFlight() *flight.FlightRecorder {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.flight
}

// SetTemplates sets the available agent templates.
func (o *Orchestrator) SetTemplates(templates []AgentTemplate) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.templates = templates
}

// SetToolAliases sets short-name to full-name mappings for MCP tools.
// e.g. "files" maps to "codegraph_files".
func (o *Orchestrator) SetToolAliases(aliases map[string]string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.toolAliases = aliases
}

// resolveToolName resolves a short tool alias to its full MCP name.
// Returns the original name if no alias mapping exists.
func (o *Orchestrator) resolveToolName(name string) string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.toolAliases == nil {
		return name
	}
	if full, ok := o.toolAliases[name]; ok {
		return full
	}
	return name
}

// GetTemplates returns available templates.
func (o *Orchestrator) GetTemplates() []AgentTemplate {
	o.mu.RLock()
	defer o.mu.RUnlock()
	result := make([]AgentTemplate, len(o.templates))
	copy(result, o.templates)
	return result
}

// CreateAgent creates and starts an agent from a request.
// Returns the agent ID immediately; the agent runs in background.
func (o *Orchestrator) CreateAgent(req AgentRequest) (string, error) {
	// Resolve template if specified.
	if req.TemplateID != "" {
		o.mu.RLock()
		for _, t := range o.templates {
			if t.ID == req.TemplateID {
				if req.Name == "" {
					req.Name = t.Name
				}
				if req.MCPTool == "" {
					req.MCPTool = t.MCPTool
				}
				if req.MCPArgs == nil {
					req.MCPArgs = t.MCPArgs
				}
				if req.LLMPrompt == "" {
					req.LLMPrompt = t.LLMPrompt
				}
				break
			}
		}
		o.mu.RUnlock()
	}

	if req.Name == "" {
		return "", fmt.Errorf("agent name is required")
	}

	id := fmt.Sprintf("agent-%d", o.nextID.Add(1))

	result := &AgentResult{
		ID:              id,
		Name:            req.Name,
		Status:          "pending",
		MCPTool:         req.MCPTool,
		StartedAt:       time.Now(),
		ResurrectionCnt: 0,
	}

	// Increment resurrection counter if this is a resume.
	if req.ResumeFrom != "" {
		o.mu.RLock()
		if prevAgent, ok := o.agents[req.ResumeFrom]; ok {
			result.ResurrectionCnt = prevAgent.ResurrectionCnt + 1
		}
		o.mu.RUnlock()
	}

	agentCtx, agentCancel := context.WithCancel(o.baseCtx)

	o.mu.Lock()
	o.agents[id] = result
	o.cancels[id] = agentCancel
	o.mu.Unlock()

	// Run in background with WaitGroup tracking and auto-resurrection.
	o.agentWg.Add(1)
	go func() {
		defer o.agentWg.Done()
		defer func() {
			o.mu.Lock()
			delete(o.cancels, id)
			o.mu.Unlock()
		}()
		o.runAgent(agentCtx, id, req, result)

		// Auto-resurrect if killed by arena (context cancelled while not completed).
		// Read result.Status and ResurrectionCnt under lock to avoid data race.
		const maxResurrections = 3
		o.mu.RLock()
		status := result.Status
		resurrectionCnt := result.ResurrectionCnt
		o.mu.RUnlock()

		if agentCtx.Err() != nil && status != "completed" && o.baseCtx.Err() == nil {
			if resurrectionCnt >= maxResurrections {
				slog.Warn(
					"orchestrator: agent exceeded max resurrections",
					"id", id,
					"name", req.Name,
					"count", resurrectionCnt,
				)
				o.emitEvent(id, "agent.resurrection_limit_exceeded", map[string]any{
					"reason": "arena kill",
					"count":  resurrectionCnt,
				})
				return
			}

			slog.Info(
				"orchestrator: agent killed, resurrecting",
				"id", id,
				"name", req.Name,
				"resurrection", resurrectionCnt+1,
			)
			o.emitEvent(id, "agent.resurrecting", map[string]any{
				"reason": "arena kill",
				"count":  resurrectionCnt + 1,
			})

			// Increment resurrection counter for the next attempt.
			req.ResumeFrom = id
			if _, err := o.CreateAgent(req); err != nil {
				slog.Error(
					"orchestrator: resurrection failed",
					"id", id,
					"error", err,
				)
			}
		}
	}()

	return id, nil
}

// GetAgent returns the current state of an agent.
func (o *Orchestrator) GetAgent(id string) (*AgentResult, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	a, ok := o.agents[id]
	if !ok {
		return nil, false
	}
	// Return a copy.
	cp := *a
	return &cp, true
}

// CancelAgent cancels a running agent by ID.
func (o *Orchestrator) CancelAgent(id string) bool {
	o.mu.RLock()
	cancel, ok := o.cancels[id]
	o.mu.RUnlock()
	if ok {
		cancel()
	}
	return ok
}

// ListAgents returns all agents.
func (o *Orchestrator) ListAgents() []AgentResult {
	o.mu.RLock()
	defer o.mu.RUnlock()
	results := make([]AgentResult, 0, len(o.agents))
	for _, a := range o.agents {
		cp := *a
		results = append(results, cp)
	}
	return results
}

// runAgent executes the full agent lifecycle: MCP → LLM → result.
func (o *Orchestrator) runAgent(ctx context.Context, id string, req AgentRequest, result *AgentResult) {
	agentStart := time.Now()

	// Record agent start in flight timeline.
	o.emitFlightTimeline(id, flight.EventAgentStart, "agent.start", agentStart, nil)

	o.updateStatus(id, "running", 10, "")
	o.emitEvent(id, "agent.started", map[string]any{"name": req.Name, "tool": req.MCPTool})
	slog.Info("orchestrator: agent started", "id", id, "name", req.Name, "tool", req.MCPTool)

	// Phase 1: MCP data gathering (single or multi-step).
	o.updateStatus(id, "gathering data...", 20, "")

	// Resume support: if ResumeFrom is set, read previous agent's ares_events to
	// determine which steps were already completed and skip them.
	startStep := 0
	var resumeSummary string
	if req.ResumeFrom != "" {
		completedSteps, summary := o.loadResumeProgress(ctx, req.ResumeFrom, req.Steps)
		startStep = completedSteps
		resumeSummary = summary
		if startStep > 0 {
			slog.Info("orchestrator: resuming agent from step",
				"id", id, "resume_from", req.ResumeFrom, "start_step", startStep+1, "total_steps", len(req.Steps))
			o.updateStatus(id, fmt.Sprintf("resuming from step %d/%d", startStep+1, len(req.Steps)), 20, "")
			o.emitEvent(id, "agent.resumed", map[string]any{
				"resume_from":  req.ResumeFrom,
				"start_step":   startStep + 1,
				"total_steps":  len(req.Steps),
				"prev_summary": resumeSummary,
			})
		}
	}

	var rawData string

	// If resuming and all MCP steps were already completed, load previous data.
	if req.ResumeFrom != "" && startStep >= len(req.Steps) && len(req.Steps) > 0 {
		prevData := o.loadPreviousData(ctx, req.ResumeFrom)
		if prevData != "" {
			rawData = prevData
			slog.Info("orchestrator: resuming with previous MCP data", "id", id, "data_len", len(rawData))
		}
	}

	// Only gather new MCP data if we don't already have it from resume.
	if rawData == "" && len(req.Steps) > 0 {
		// Multi-step: call each tool in sequence, accumulate results.
		// Skip steps that were already completed by the previous agent.
		var sb strings.Builder
		sb.Grow(len(req.Steps) * 4096)
		for i, step := range req.Steps {
			if i < startStep {
				// Already completed by the previous agent; skip.
				continue
			}
			select {
			case <-ctx.Done():
				o.failAgent(id, ctx.Err())
				return
			default:
			}

			progress := 20 + ((i + 1) * 25 / len(req.Steps))
			o.updateStatus(id, fmt.Sprintf("step %d/%d: %s", i+1, len(req.Steps), step.Tool), progress, "")
			toolName := o.resolveToolName(step.Tool)
			o.emitFlightDecision(id, toolName, fmt.Sprintf("step %d", i+1))

			mcpStart := time.Now()
			o.emitFlightTimeline(id, flight.EventToolCall, "mcp.call."+toolName, mcpStart, map[string]any{"step": i + 1})

			res, err := o.mcp.CallTool(ctx, toolName, step.Args)
			if err != nil {
				o.emitFlightTimeline(id, flight.EventError, "mcp.call."+toolName+".error", mcpStart, map[string]any{"error": err.Error()})
				o.failAgent(id, err)
				return
			}

			o.emitFlightTimelineEnd(id, flight.EventToolResult, "mcp.call."+toolName, mcpStart)

			// Record step completion in event store for future resurrection.
			o.emitEvent(id, "mcp.step.completed", map[string]any{
				"step":  i + 1,
				"tool":  toolName,
				"args":  step.Args,
				"total": len(req.Steps),
			})

			for _, b := range res.Content {
				fmt.Fprintf(&sb, "\n--- Step %d: %s ---\n%s\n", i+1, toolName, b.Text)
			}
		}
		rawData = sb.String()
	} else if rawData == "" && req.MCPTool == "" {
		// List tools.
		mcpStart := time.Now()
		o.emitFlightTimeline(id, flight.EventToolCall, "mcp.list_tools", mcpStart, nil)

		tools, err := o.mcp.ListTools(ctx)
		if err != nil {
			o.emitFlightTimeline(id, flight.EventError, "mcp.list_tools.error", mcpStart, map[string]any{"error": err.Error()})
			o.failAgent(id, err)
			return
		}

		o.emitFlightTimelineEnd(id, flight.EventToolResult, "mcp.list_tools", mcpStart)
		data, _ := json.MarshalIndent(tools, "", "  ")
		rawData = string(data)
	} else if rawData == "" {
		// Single tool call.
		mcpToolName := o.resolveToolName(req.MCPTool)
		o.emitFlightDecision(id, mcpToolName, "template selection")

		mcpStart := time.Now()
		o.emitFlightTimeline(id, flight.EventToolCall, "mcp.call."+mcpToolName, mcpStart, map[string]any{"tool": mcpToolName})

		res, err := o.mcp.CallTool(ctx, mcpToolName, req.MCPArgs)
		if err != nil {
			o.emitFlightTimeline(id, flight.EventError, "mcp.call."+mcpToolName+".error", mcpStart, map[string]any{"error": err.Error()})
			o.failAgent(id, err)
			return
		}

		o.emitFlightTimelineEnd(id, flight.EventToolResult, "mcp.call."+mcpToolName, mcpStart)
		for _, b := range res.Content {
			rawData += b.Text
		}
	}

	o.updateRawDataLen(id, len(rawData))
	const maxStoredData = 10 * 1024
	storedData := rawData
	if len(storedData) > maxStoredData {
		storedData = storedData[:maxStoredData]
	}
	o.emitEvent(id, "mcp.data.gathered", map[string]any{"bytes": len(rawData), "data": storedData})
	o.updateStatus(id, "analyzing with LLM...", 50, "")
	slog.Info("orchestrator: MCP data gathered", "id", id, "bytes", len(rawData))

	// Phase 2: LLM analysis.
	// Prepend resume context to the prompt so the LLM knows what was already done.
	prompt := req.LLMPrompt
	if resumeSummary != "" && startStep > 0 {
		resumeCtx := fmt.Sprintf("This agent was interrupted and is being resumed.\nPrevious progress:\n%s\nContinuing from step %d...\n\n", resumeSummary, startStep+1)
		prompt = resumeCtx + prompt
	}
	if prompt == "" {
		prompt = "Analyze the following code data and provide insights:\n\n{{.raw_data}}"
	}

	// Render template using TemplateEngine; fall back to simple replacement
	// if the template syntax is malformed.
	truncated := truncateStr(rawData, 8000)
	engine := output.NewTemplateEngine()
	rendered, err := engine.Render(prompt, map[string]string{"raw_data": truncated})
	if err != nil {
		// Fallback: plain string replacement for malformed templates.
		if strings.Contains(prompt, "{{.raw_data}}") {
			prompt = strings.ReplaceAll(prompt, "{{.raw_data}}", truncated)
		} else {
			prompt = prompt + "\n\nData:\n" + truncated
		}
	} else {
		prompt = rendered
	}

	// Try streaming first; fall back to blocking Generate.
	llmStart := time.Now()
	o.emitFlightTimeline(id, flight.EventLLMCall, "llm.generate", llmStart, nil)

	analysis, err := o.llmGenerateStreaming(ctx, id, prompt)
	if err != nil {
		o.emitFlightTimeline(id, flight.EventError, "llm.generate.error", llmStart, map[string]any{"error": err.Error()})
		o.failAgent(id, err)
		return
	}

	o.emitFlightTimelineEnd(id, flight.EventLLMResult, "llm.generate", llmStart)

	// Phase 3: Done.
	o.mu.Lock()
	result.Status = "completed"
	result.Progress = 100
	result.Analysis = analysis
	result.FinishedAt = time.Now()
	result.Duration = result.FinishedAt.Sub(result.StartedAt).Round(time.Second).String()
	cp := *result
	o.mu.Unlock()

	// Record agent end in flight timeline.
	o.emitFlightTimeline(id, flight.EventAgentEnd, "agent.end", agentStart, map[string]any{
		"duration": result.Duration,
	})

	slog.Info("orchestrator: agent completed", "id", id, "duration", result.Duration)
	o.emitEvent(id, "agent.completed", map[string]any{"duration": result.Duration, "analysis_len": len(analysis)})

	// Broadcast completion.
	hub := o.getHub()
	if hub != nil {
		hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: &cp,
		})
		// Also broadcast to the agent-specific channel for result viewers.
		hub.BroadcastToChannel("agent:"+id, &WSMessage{
			Type: "agent_result",
			Data: &cp,
		})
	}
}

// llmGenerateStreaming attempts streaming via StreamLLMExecutor, falling back to Generate.
func (o *Orchestrator) llmGenerateStreaming(ctx context.Context, agentID, prompt string) (string, error) {
	if streamer, ok := o.llm.(StreamLLMExecutor); ok {
		ch, err := streamer.GenerateStream(ctx, prompt)
		if err == nil {
			return o.consumeStream(ctx, agentID, ch)
		}
		// Streaming init failed — fall through to blocking call.
		slog.Warn("orchestrator: GenerateStream failed, falling back to Generate", "id", agentID, "error", err)
	}
	return o.llm.Generate(ctx, prompt)
}

// consumeStream reads chunks from the channel, accumulates the analysis, and
// broadcasts each chunk via WebSocket. Returns the full accumulated text.
// If ctx is cancelled before the stream finishes, returns ctx.Err() with
// whatever analysis has been accumulated so far.
func (o *Orchestrator) consumeStream(ctx context.Context, agentID string, ch <-chan StreamChunk) (string, error) {
	var analysis string
	for {
		select {
		case <-ctx.Done():
			return analysis, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				return analysis, nil
			}
			if chunk.Err != nil {
				return analysis, chunk.Err
			}
			if chunk.Content != "" {
				analysis += chunk.Content
				o.broadcastStreamChunk(agentID, chunk.Content)
			}
			if chunk.Done {
				return analysis, nil
			}
		}
	}
}

// broadcastStreamChunk sends a single streaming chunk to WebSocket subscribers.
func (o *Orchestrator) broadcastStreamChunk(agentID, content string) {
	hub := o.getHub()
	if hub == nil {
		return
	}
	hub.BroadcastToChannel("agent:"+agentID, &WSMessage{
		Type: WSTypeAgentStream,
		Data: map[string]string{"chunk": content},
	})
}

func (o *Orchestrator) updateStatus(id, status string, progress int, errMsg string) {
	o.mu.Lock()
	var agentCopy *AgentResult
	if a, ok := o.agents[id]; ok {
		a.Status = status
		a.Progress = progress
		if errMsg != "" {
			a.Error = errMsg
		}
		cp := *a
		agentCopy = &cp
	}
	o.mu.Unlock()

	// Broadcast to WebSocket subscribers.
	hub := o.getHub()
	if hub != nil && agentCopy != nil {
		hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: agentCopy,
		})
	}
}

func (o *Orchestrator) updateRawDataLen(id string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if a, ok := o.agents[id]; ok {
		a.RawDataLen = n
	}
}

func (o *Orchestrator) failAgent(id string, err error) {
	o.mu.Lock()
	var cp *AgentResult
	var duration time.Duration
	if a, ok := o.agents[id]; ok {
		a.Status = "failed"
		a.Error = err.Error()
		a.FinishedAt = time.Now()
		a.Duration = a.FinishedAt.Sub(a.StartedAt).Round(time.Second).String()
		duration = a.FinishedAt.Sub(a.StartedAt)
		tmp := *a
		cp = &tmp
	}
	o.mu.Unlock()

	// Record failure diagnostics in flight recorder.
	if fr := o.getFlight(); fr != nil {
		fr.Diagnostics().Record(flight.AutoDiagnose(id, "", err, duration))
	}

	slog.Error("orchestrator: agent failed", "id", id, "error", err)
	o.emitEvent(id, "agent.failed", map[string]any{"error": err.Error()})

	hub := o.getHub()
	if hub != nil && cp != nil {
		hub.BroadcastToChannel(WSChannelAgents, &WSMessage{
			Type: WSTypeAgentUpdate,
			Data: cp,
		})
	}
}

// emitEvent stores an event using the canonical ares_events.Emit.
func (o *Orchestrator) emitEvent(streamID, eventType string, payload map[string]any) {
	store := o.getStore()
	if store == nil {
		return
	}
	if !ares_events.Emit(context.Background(), store, streamID, ares_events.EventType(eventType), "dashboard", payload) {
		slog.Warn("failed to emit event", "event_type", eventType, "stream_id", streamID)
	}
}

// getHub returns the current hub under a read lock. Safe for concurrent use.
func (o *Orchestrator) getHub() *WSHub {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.hub
}

// getStore returns the current event store under a read lock. Safe for concurrent use.
func (o *Orchestrator) getStore() *ares_events.MemoryEventStore {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.store
}

// emitFlightTimeline adds a start-style TimelineEvent to the flight recorder.
// Nil-safe: does nothing if the flight recorder is not configured.
func (o *Orchestrator) emitFlightTimeline(agentID string, eventType flight.EventType, name string, startAt time.Time, metadata map[string]any) {
	fr := o.getFlight()
	if fr == nil {
		return
	}
	fr.Timeline().Add(flight.TimelineEvent{
		ID:       fmt.Sprintf("ft-%d", time.Now().UnixNano()),
		AgentID:  agentID,
		Type:     eventType,
		Name:     name,
		StartAt:  startAt,
		Metadata: metadata,
	})
}

// emitFlightTimelineEnd adds a completed-style TimelineEvent with duration.
// Nil-safe: does nothing if the flight recorder is not configured.
func (o *Orchestrator) emitFlightTimelineEnd(agentID string, eventType flight.EventType, name string, startAt time.Time) {
	fr := o.getFlight()
	if fr == nil {
		return
	}
	now := time.Now()
	fr.Timeline().Add(flight.TimelineEvent{
		ID:       fmt.Sprintf("ft-%d", now.UnixNano()),
		AgentID:  agentID,
		Type:     eventType,
		Name:     name,
		StartAt:  startAt,
		EndAt:    now,
		Duration: now.Sub(startAt),
	})
}

// emitFlightDecision records a tool selection decision in the flight recorder.
// Nil-safe: does nothing if the flight recorder is not configured.
func (o *Orchestrator) emitFlightDecision(agentID, selected, reason string) {
	fr := o.getFlight()
	if fr == nil {
		return
	}
	o.mu.RLock()
	availableTools := make([]string, len(o.templates))
	for i, t := range o.templates {
		availableTools[i] = t.MCPTool
	}
	o.mu.RUnlock()

	fr.Decisions().Add(flight.Decision{
		ID:         fmt.Sprintf("dec-%s", agentID),
		AgentID:    agentID,
		Type:       flight.DecisionToolSelect,
		Candidates: availableTools,
		Selected:   selected,
		Reason:     reason,
		Timestamp:  time.Now(),
	})
}

// loadResumeProgress reads ares_events from a previous agent to determine how many
// multi-step calls were already completed and builds a human-readable summary.
// Returns the number of completed steps and a summary string.
// Nil-safe: returns (0, "") if the event store is not configured or no ares_events are found.
func (o *Orchestrator) loadResumeProgress(ctx context.Context, previousAgentID string, steps []AgentStep) (int, string) {
	store := o.getStore()
	if store == nil {
		return 0, ""
	}

	prevEvents, err := store.Read(ctx, previousAgentID, ares_events.ReadOptions{
		Direction: ares_events.ReadAscending,
		Limit:     10000,
	})
	if err != nil {
		slog.Warn("orchestrator: failed to read resume ares_events", "agent", previousAgentID, "error", err)
		return 0, ""
	}

	// Count mcp.step.completed ares_events to determine how far the previous agent got.
	completedSteps := 0
	var completedDetails []string
	for _, evt := range prevEvents {
		if evt.Type != "mcp.step.completed" {
			continue
		}
		completedSteps++
		var stepNum int
		switch v := evt.Payload["step"].(type) {
		case int:
			stepNum = v
		case float64:
			stepNum = int(v)
		}
		toolName, _ := evt.Payload["tool"].(string)
		completedDetails = append(completedDetails, fmt.Sprintf("  - Step %d (%s): completed", stepNum, toolName))
	}

	if completedSteps == 0 {
		return 0, ""
	}

	// Cap to the actual step count in case the event list is stale.
	if completedSteps > len(steps) {
		completedSteps = len(steps)
	}

	summary := fmt.Sprintf("Agent %s completed %d/%d steps before interruption:\n%s",
		previousAgentID, completedSteps, len(steps), strings.Join(completedDetails, "\n"))

	return completedSteps, summary
}

// loadPreviousData loads the raw MCP data from a previous agent's ares_events.
// Used when resuming: all MCP steps completed, need the data for LLM.
func (o *Orchestrator) loadPreviousData(ctx context.Context, previousAgentID string) string {
	store := o.getStore()
	if store == nil {
		return ""
	}

	evts, err := store.Read(ctx, previousAgentID, ares_events.ReadOptions{
		Direction: ares_events.ReadAscending,
		Limit:     10000,
	})
	if err != nil {
		return ""
	}

	// Find the largest mcp.data.gathered event — that's the final accumulated data.
	var maxLen int
	var maxData string
	for _, evt := range evts {
		if evt.Type == "mcp.data.gathered" {
			if data, ok := evt.Payload["data"].(string); ok && len(data) > maxLen {
				maxLen = len(data)
				maxData = data
			}
		}
	}

	return maxData
}

// BuildToolAliases creates short-name mappings from MCP tool definitions.
// For a tool named "codegraph_files", it maps both "codegraph_files" and "files" to "codegraph_files".
func BuildToolAliases(tools []MCPToolInfo) map[string]string {
	aliases := make(map[string]string)
	for _, t := range tools {
		aliases[t.Name] = t.Name
		if idx := strings.Index(t.Name, "_"); idx >= 0 {
			aliases[t.Name[idx+1:]] = t.Name
		}
	}
	return aliases
}

func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
