package dashboard

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		// Headers already sent; log at Debug level since there is nothing we can do.
		slog.Debug("dashboard: failed to encode JSON response", "error", err)
	}
}
