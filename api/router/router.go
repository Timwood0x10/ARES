// Package router provides HTTP routing for the GoAgent API.
package router

import (
	"net/http"

	"github.com/Timwood0x10/ares/api/handler"
	"github.com/Timwood0x10/ares/internal/agents/base"
)

// Router provides HTTP routing for the API.
type Router struct {
	mux     *http.ServeMux
	streamH *handler.StreamHandler
	evoH    *handler.EvolutionHandler
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

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Handler returns the underlying http.Handler.
func (r *Router) Handler() http.Handler {
	return r.mux
}
