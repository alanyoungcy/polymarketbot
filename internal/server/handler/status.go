package handler

import (
	"net/http"
)

// StatusHandler serves the backend status (mode, strategy) for the dashboard.
type StatusHandler struct {
	Mode         string
	StrategyName string
}

// NewStatusHandler creates a StatusHandler with the given mode and strategy name.
func NewStatusHandler(mode, strategyName string) *StatusHandler {
	return &StatusHandler{Mode: mode, StrategyName: strategyName}
}

// GetStatus responds with the current backend mode and strategy name.
// GET /api/status
func (h *StatusHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":           h.Mode,
		"strategy_name":  h.StrategyName,
	})
}
