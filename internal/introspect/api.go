package introspect

import (
	"embed"
	"encoding/json"
	"net/http"
	"strconv"
)

//go:embed web/panel.html
var webFS embed.FS

// Handler serves the introspection panel: the embedded UI at /introspect and
// the JSON read API under /api/v1/introspect/*.
type Handler struct {
	store *Store
}

// NewHandler builds a Handler over the given store (must be non-nil).
func NewHandler(store *Store) *Handler {
	return &Handler{store: store}
}

// ServeHTTP routes introspection requests. Register it on the serve mux for
// the /introspect and /api/v1/introspect prefixes.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/introspect", "/introspect/":
		body, err := webFS.ReadFile("web/panel.html")
		if err != nil {
			http.Error(w, "panel asset missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(body)
	case "/api/v1/introspect/events":
		limit := 60
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= maxTimelineEntries {
				limit = n
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"events": h.store.Events(limit)})
	case "/api/v1/introspect/snapshot":
		snap := h.store.Latest()
		w.Header().Set("Content-Type", "application/json")
		if snap == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"collector has not produced a snapshot yet"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(snap)
	default:
		http.NotFound(w, r)
	}
}
