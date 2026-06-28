package evalapi

import (
	"net/http"

	"github.com/Timwood0x10/ares/api/router"
)

// RegisterRoutes registers all eval API routes on the given router.
// This follows the existing project pattern of registering routes
// on the shared ServeMux-based router.
//
// Args:
//
//	r - the application router to register routes on.
//	h - the eval handler instance.
//
// Returns error is always nil (kept for future extensibility).
func RegisterRoutes(r *router.Router, h *Handler) error {
	mux := r.Handler().(*http.ServeMux)

	// Evaluation run endpoints.
	mux.HandleFunc("POST /api/v1/eval/run", h.HandleRunEval)
	mux.HandleFunc("GET /api/v1/eval/results/{run_id}", h.HandleGetResults)

	// Leaderboard endpoint.
	mux.HandleFunc("GET /api/v1/eval/leaderboard", h.HandleGetLeaderboard)

	// Comparison endpoint.
	mux.HandleFunc("GET /api/v1/eval/comparison", h.HandleGetComparison)

	return nil
}
