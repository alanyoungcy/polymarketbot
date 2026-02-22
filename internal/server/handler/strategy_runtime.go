package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// StrategyRuntimeController is the interface for getting/setting the active
// strategy at runtime (e.g. strategy.Engine in trade/full mode).
type StrategyRuntimeController interface {
	ActiveName() string
	ListNames() []string
	SetActive(name string) error
}

// HubStrategyUpdater is called when the active strategy is changed so the
// WebSocket hub can report the new name in bot_status.
type HubStrategyUpdater interface {
	SetStrategyName(name string)
}

// StrategyRuntimeHandler serves GET/POST /api/strategy/active and GET /api/strategy/list.
// When ctrl is nil (e.g. arbitrage-only mode), requests return 501.
type StrategyRuntimeHandler struct {
	ctrl  StrategyRuntimeController
	hub   HubStrategyUpdater // optional; when set, updated on POST
	logger *slog.Logger
}

// NewStrategyRuntimeHandler creates a handler. ctrl may be nil.
func NewStrategyRuntimeHandler(ctrl StrategyRuntimeController, hub HubStrategyUpdater, logger *slog.Logger) *StrategyRuntimeHandler {
	return &StrategyRuntimeHandler{ctrl: ctrl, hub: hub, logger: logger}
}

// GetActive returns the current active strategy name.
// GET /api/strategy/active
func (h *StrategyRuntimeHandler) GetActive(w http.ResponseWriter, r *http.Request) {
	if h.ctrl == nil {
		writeError(w, http.StatusNotImplemented, "strategy runtime not available (arbitrage-only or scrape mode)")
		return
	}
	active := h.ctrl.ActiveName()
	writeJSON(w, http.StatusOK, map[string]string{"active": active})
}

// List returns all registered strategy names.
// GET /api/strategy/list
func (h *StrategyRuntimeHandler) List(w http.ResponseWriter, r *http.Request) {
	if h.ctrl == nil {
		writeError(w, http.StatusNotImplemented, "strategy runtime not available (arbitrage-only or scrape mode)")
		return
	}
	names := h.ctrl.ListNames()
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"strategies": names})
}

// SetActiveRequest is the JSON body for POST /api/strategy/active.
type SetActiveRequest struct {
	Name string `json:"name"`
}

// SetActive sets the active strategy and returns 200 or 400.
// POST /api/strategy/active
func (h *StrategyRuntimeHandler) SetActive(w http.ResponseWriter, r *http.Request) {
	if h.ctrl == nil {
		writeError(w, http.StatusNotImplemented, "strategy runtime not available (arbitrage-only or scrape mode)")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req SetActiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := h.ctrl.SetActive(name); err != nil {
		h.logger.WarnContext(r.Context(), "set active strategy failed",
			slog.String("name", name),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.hub != nil {
		h.hub.SetStrategyName(name)
	}
	writeJSON(w, http.StatusOK, map[string]string{"active": name})
}
