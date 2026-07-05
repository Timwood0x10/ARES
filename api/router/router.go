// Package router provides HTTP routing for the GoAgent API.
package router

import (
	"net/http"

	"github.com/Timwood0x10/ares/api/handler"
	"github.com/Timwood0x10/ares/internal/agents/base"
)

// Router provides HTTP routing for the API.
type Router struct {
	mux        *http.ServeMux
	streamH    *handler.StreamHandler
	evoH       *handler.EvolutionHandler
	workflowH  *handler.WorkflowHandler
	agentH     *handler.AgentHandler
	memoryH    *handler.MemoryHandler
	arenaH     *handler.ArenaHandler
	runtimeH   *handler.RuntimeHandler
	retrievalH *handler.RetrievalHandler
	evalH      *handler.EvalHandler
	flightH    *handler.FlightHandler
}

// NewRouter creates a new router.
func NewRouter() *Router {
	return &Router{
		mux:     http.NewServeMux(),
		streamH: handler.NewStreamHandler(),
	}
}

// AgentProcessorFunc is an adapter to allow using a function as AgentProcessor.
type AgentProcessorFunc func(ctx any, input any) (<-chan base.AgentEvent, error)

// RegisterStreamEndpoint registers the streaming endpoint with a processor.
func (r *Router) RegisterStreamEndpoint(processor handler.AgentProcessor) {
	r.mux.HandleFunc("POST /api/v1/stream", r.streamH.HandleStream(processor))
}

// RegisterEvolutionEndpoints registers evolution HTTP endpoints.
func (r *Router) RegisterEvolutionEndpoints(evolutionHandler *handler.EvolutionHandler) {
	r.evoH = evolutionHandler
	r.mux.HandleFunc("POST /api/v1/evolution/start", r.evoH.HandleStart)
	r.mux.HandleFunc("POST /api/v1/evolution/idle", r.evoH.HandleIdleStart)
	r.mux.HandleFunc("GET /api/v1/evolution/report", r.evoH.HandleReport)
	r.mux.HandleFunc("GET /api/v1/evolution/status", r.evoH.HandleStatus)
}

// RegisterWorkflowEndpoints registers workflow HTTP endpoints.
func (r *Router) RegisterWorkflowEndpoints(workflowHandler *handler.WorkflowHandler) {
	r.workflowH = workflowHandler
	r.mux.HandleFunc("POST /api/v1/workflows/execute", r.workflowH.HandleExecute)
	r.mux.HandleFunc("GET /api/v1/workflows", r.workflowH.HandleList)
	r.mux.HandleFunc("GET /api/v1/workflows/{id}", r.workflowH.HandleGet)
}

// RegisterAgentEndpoints registers agent HTTP endpoints.
func (r *Router) RegisterAgentEndpoints(agentHandler *handler.AgentHandler) {
	r.agentH = agentHandler
	r.mux.HandleFunc("POST /api/v1/agents", r.agentH.HandleCreate)
	r.mux.HandleFunc("GET /api/v1/agents", r.agentH.HandleList)
	r.mux.HandleFunc("GET /api/v1/agents/{id}", r.agentH.HandleGet)
	r.mux.HandleFunc("DELETE /api/v1/agents/{id}", r.agentH.HandleDelete)
}

// RegisterMemoryEndpoints registers memory HTTP endpoints.
func (r *Router) RegisterMemoryEndpoints(memoryHandler *handler.MemoryHandler) {
	r.memoryH = memoryHandler
	r.mux.HandleFunc("POST /api/v1/sessions", r.memoryH.HandleCreateSession)
	r.mux.HandleFunc("GET /api/v1/sessions/{id}", r.memoryH.HandleGetSession)
	r.mux.HandleFunc("DELETE /api/v1/sessions/{id}", r.memoryH.HandleDeleteSession)
	r.mux.HandleFunc("POST /api/v1/sessions/{id}/messages", r.memoryH.HandleAddMessage)
	r.mux.HandleFunc("GET /api/v1/sessions/{id}/messages", r.memoryH.HandleGetMessages)
}

// RegisterArenaEndpoints registers arena chaos engineering HTTP endpoints.
func (r *Router) RegisterArenaEndpoints(arenaHandler *handler.ArenaHandler) {
	r.arenaH = arenaHandler
	r.mux.HandleFunc("POST /api/v1/arena/faults", r.arenaH.HandleInjectFault)
	r.mux.HandleFunc("GET /api/v1/arena/score", r.arenaH.HandleScore)
	r.mux.HandleFunc("POST /api/v1/arena/random", r.arenaH.HandleRunRandom)
	r.mux.HandleFunc("GET /api/v1/arena/agents", r.arenaH.HandleListAgents)
}

// RegisterRuntimeEndpoints registers runtime HTTP endpoints.
func (r *Router) RegisterRuntimeEndpoints(runtimeHandler *handler.RuntimeHandler) {
	r.runtimeH = runtimeHandler
	r.mux.HandleFunc("POST /api/v1/runtime/start", r.runtimeH.HandleStart)
	r.mux.HandleFunc("POST /api/v1/runtime/stop", r.runtimeH.HandleStop)
	r.mux.HandleFunc("GET /api/v1/runtime/agents/{id}", r.runtimeH.HandleGetAgent)
	r.mux.HandleFunc("GET /api/v1/runtime/stats", r.runtimeH.HandleStats)
}

// RegisterRetrievalEndpoints registers knowledge retrieval HTTP endpoints.
func (r *Router) RegisterRetrievalEndpoints(retrievalHandler *handler.RetrievalHandler) {
	r.retrievalH = retrievalHandler
	r.mux.HandleFunc("POST /api/v1/knowledge/search", r.retrievalH.HandleSearch)
	r.mux.HandleFunc("POST /api/v1/knowledge", r.retrievalH.HandleAddKnowledge)
	r.mux.HandleFunc("GET /api/v1/knowledge/{tenant_id}/{id}", r.retrievalH.HandleGetKnowledge)
	r.mux.HandleFunc("DELETE /api/v1/knowledge/{tenant_id}/{id}", r.retrievalH.HandleDeleteKnowledge)
}

// RegisterEvalEndpoints registers evaluation HTTP endpoints.
func (r *Router) RegisterEvalEndpoints(evalHandler *handler.EvalHandler) {
	r.evalH = evalHandler
	r.mux.HandleFunc("POST /api/v1/eval/evaluate", r.evalH.HandleEvaluate)
	r.mux.HandleFunc("GET /api/v1/eval/evaluators", r.evalH.HandleListEvaluators)
}

// RegisterFlightEndpoints registers flight recorder HTTP endpoints.
func (r *Router) RegisterFlightEndpoints(flightHandler *handler.FlightHandler) {
	r.flightH = flightHandler
	r.mux.HandleFunc("GET /api/v1/flight/replay/{id}", r.flightH.HandleReplay)
	r.mux.HandleFunc("POST /api/v1/flight/stop", r.flightH.HandleStop)
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Handler returns the underlying http.Handler.
func (r *Router) Handler() http.Handler {
	return r.mux
}
