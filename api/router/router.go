// Package router provides HTTP routing for the ARES API.
package router

import (
	"crypto/subtle"
	"net/http"

	"github.com/Timwood0x10/ares/api/handler"
)

const (
	// methodPOST is the HTTP POST method constant used in route registrations.
	methodPOST = "POST"
	// methodGET is the HTTP GET method constant used in route registrations.
	methodGET = "GET"
	// methodDELETE is the HTTP DELETE method constant used in route registrations.
	methodDELETE = "DELETE"
)

// defaultAPIKey is a development-only placeholder.
// Production deployments MUST override this via WithAPIKey.
// An empty key means "auth disabled" (development mode); a non-empty key
// gates all endpoints via X-API-Key header comparison with constant-time
// equality.
const defaultAPIKey = "change-me-in-production"

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
	llmH       *handler.LLMHandler
	apiKey     string
}

// NewRouter creates a new router with the default API key.
// Use WithAPIKey to override the key for production.
func NewRouter() *Router {
	return &Router{
		mux:     http.NewServeMux(),
		streamH: handler.NewStreamHandler(),
		apiKey:  defaultAPIKey,
	}
}

// WithAPIKey sets the API key used for endpoint authentication.
func (r *Router) WithAPIKey(key string) *Router {
	if key != "" {
		r.apiKey = key
	}
	return r
}

// authMiddleware wraps an http.HandlerFunc with API key authentication.
// The key must be provided via the X-API-Key header.
func (r *Router) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		provided := req.Header.Get("X-API-Key")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(r.apiKey)) != 1 {
			http.Error(w, "401: unauthorized — provide X-API-Key header", http.StatusUnauthorized)
			return
		}
		next(w, req)
	}
}

// RegisterStreamEndpoint registers the streaming endpoint with a processor.
func (r *Router) RegisterStreamEndpoint(processor handler.AgentProcessor) {
	r.mux.HandleFunc("POST /api/v1/stream", r.authMiddleware(r.streamH.HandleStream(processor)))
}

// RegisterEvolutionEndpoints registers evolution HTTP endpoints.
func (r *Router) RegisterEvolutionEndpoints(evolutionHandler *handler.EvolutionHandler) {
	r.evoH = evolutionHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/evolution/start", r.evoH.HandleStart},
		{methodPOST, "/api/v1/evolution/idle", r.evoH.HandleIdleStart},
		{methodGET, "/api/v1/evolution/report", r.evoH.HandleReport},
		{methodGET, "/api/v1/evolution/status", r.evoH.HandleStatus},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterRuntimeEvolutionEndpoints registers runtime evolution HTTP endpoints.
func (r *Router) RegisterRuntimeEvolutionEndpoints(handler *handler.RuntimeEvolutionHandler) {
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/evolution/runtime/cycle", handler.HandleCycle},
		{methodGET, "/api/v1/evolution/runtime/status", handler.HandleRuntimeStatus},
		{methodPOST, "/api/v1/evolution/runtime/propose", handler.HandlePropose},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterWorkflowEndpoints registers workflow HTTP endpoints.
func (r *Router) RegisterWorkflowEndpoints(workflowHandler *handler.WorkflowHandler) {
	r.workflowH = workflowHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/workflows/execute", r.workflowH.HandleExecute},
		{methodGET, "/api/v1/workflows", r.workflowH.HandleList},
		{methodGET, "/api/v1/workflows/{id}", r.workflowH.HandleGet},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterAgentEndpoints registers agent HTTP endpoints.
func (r *Router) RegisterAgentEndpoints(agentHandler *handler.AgentHandler) {
	r.agentH = agentHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/agents", r.agentH.HandleCreate},
		{methodGET, "/api/v1/agents", r.agentH.HandleList},
		{methodGET, "/api/v1/agents/{id}", r.agentH.HandleGet},
		{methodDELETE, "/api/v1/agents/{id}", r.agentH.HandleDelete},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterMemoryEndpoints registers memory HTTP endpoints.
func (r *Router) RegisterMemoryEndpoints(memoryHandler *handler.MemoryHandler) {
	r.memoryH = memoryHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/sessions", r.memoryH.HandleCreateSession},
		{methodGET, "/api/v1/sessions/{id}", r.memoryH.HandleGetSession},
		{methodDELETE, "/api/v1/sessions/{id}", r.memoryH.HandleDeleteSession},
		{methodPOST, "/api/v1/sessions/{id}/messages", r.memoryH.HandleAddMessage},
		{methodGET, "/api/v1/sessions/{id}/messages", r.memoryH.HandleGetMessages},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterArenaEndpoints registers arena chaos engineering HTTP endpoints.
func (r *Router) RegisterArenaEndpoints(arenaHandler *handler.ArenaHandler) {
	r.arenaH = arenaHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/arena/faults", r.arenaH.HandleInjectFault},
		{methodGET, "/api/v1/arena/score", r.arenaH.HandleScore},
		{methodPOST, "/api/v1/arena/random", r.arenaH.HandleRunRandom},
		{methodGET, "/api/v1/arena/agents", r.arenaH.HandleListAgents},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterRuntimeEndpoints registers runtime HTTP endpoints.
func (r *Router) RegisterRuntimeEndpoints(runtimeHandler *handler.RuntimeHandler) {
	r.runtimeH = runtimeHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/runtime/start", r.runtimeH.HandleStart},
		{methodPOST, "/api/v1/runtime/stop", r.runtimeH.HandleStop},
		{methodGET, "/api/v1/runtime/agents/{id}", r.runtimeH.HandleGetAgent},
		{methodGET, "/api/v1/runtime/stats", r.runtimeH.HandleStats},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterRetrievalEndpoints registers knowledge retrieval HTTP endpoints.
func (r *Router) RegisterRetrievalEndpoints(retrievalHandler *handler.RetrievalHandler) {
	r.retrievalH = retrievalHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/knowledge/search", r.retrievalH.HandleSearch},
		{methodPOST, "/api/v1/knowledge", r.retrievalH.HandleAddKnowledge},
		{methodGET, "/api/v1/knowledge/{tenant_id}/{id}", r.retrievalH.HandleGetKnowledge},
		{methodDELETE, "/api/v1/knowledge/{tenant_id}/{id}", r.retrievalH.HandleDeleteKnowledge},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterEvalEndpoints registers evaluation HTTP endpoints.
func (r *Router) RegisterEvalEndpoints(evalHandler *handler.EvalHandler) {
	r.evalH = evalHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/eval/evaluate", r.evalH.HandleEvaluate},
		{methodGET, "/api/v1/eval/evaluators", r.evalH.HandleListEvaluators},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterFlightEndpoints registers flight recorder HTTP endpoints.
func (r *Router) RegisterFlightEndpoints(flightHandler *handler.FlightHandler) {
	r.flightH = flightHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodGET, "/api/v1/flight/replay/{id}", r.flightH.HandleReplay},
		{methodPOST, "/api/v1/flight/stop", r.flightH.HandleStop},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// RegisterLLMEndpoints registers LLM inference HTTP endpoints.
func (r *Router) RegisterLLMEndpoints(llmHandler *handler.LLMHandler) {
	r.llmH = llmHandler
	for _, route := range []struct {
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{methodPOST, "/api/v1/llm/chat", r.llmH.HandleChat},
		{methodPOST, "/api/v1/llm/generate", r.llmH.HandleGenerateSimple},
	} {
		r.mux.HandleFunc(route.method+" "+route.path, r.authMiddleware(route.fn))
	}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Handler returns the underlying http.Handler.
func (r *Router) Handler() http.Handler {
	return r.mux
}
