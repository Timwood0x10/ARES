// Package core provides core interfaces for the ARES system.
package core

import "net/http"

// Dashboard defines the interface for the monitoring dashboard.
type Dashboard interface {
	// Handler returns the HTTP handler for dashboard routes.
	// Returns the HTTP handler.
	Handler() http.Handler
}
