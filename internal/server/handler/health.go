package handler

import (
	"log/slog"
	"net/http"
	"time"
)

// HealthHandler serves the health-check endpoint.
type HealthHandler struct {
	logger *slog.Logger
}

// NewHealthHandler creates a HealthHandler with the provided logger.
func NewHealthHandler(logger *slog.Logger) *HealthHandler {
	return &HealthHandler{logger: logger}
}

// HealthCheck responds with a simple JSON status indicating the server is alive.
// GET /api/health
func (h *HealthHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
}
