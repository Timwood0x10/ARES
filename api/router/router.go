// Package router provides HTTP routing for the GoAgent API.
package router

import (
	"net/http"

	"goagentx/api/handler"
	"goagentx/internal/agents/base"
)

// Router provides HTTP routing for the API.
type Router struct {
	mux     *http.ServeMux
	streamH *handler.StreamHandler
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

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// Handler returns the underlying http.Handler.
func (r *Router) Handler() http.Handler {
	return r.mux
}
