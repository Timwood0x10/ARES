// Package handler provides shared utilities for HTTP handlers.
package handler

import (
	"encoding/json"
	"net/http"

	"github.com/Timwood0x10/ares/internal/logger"
)

// log is the package-level structured logger.
var log = logger.Module("handler")

// Common response field names shared across all handlers.
const (
	keyStatus  = "status"
	keyDeleted = "deleted"
)

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Header is already committed — cannot send an HTTP error.
		// Log so the encoding failure is at least observable.
		log.Error("encode json response", "error", err, "status", status)
	}
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
