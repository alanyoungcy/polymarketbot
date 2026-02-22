package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// StrategyConfigService defines the methods that the strategy handler requires.
type StrategyConfigService interface {
	Get(ctx context.Context, name string) (domain.StrategyConfig, error)
	Upsert(ctx context.Context, cfg domain.StrategyConfig) error
	List(ctx context.Context) ([]domain.StrategyConfig, error)
}

// StrategyHandler serves strategy configuration HTTP endpoints.
type StrategyHandler struct {
	configs StrategyConfigService
	logger  *slog.Logger
}

// NewStrategyHandler creates a StrategyHandler with the given service and logger.
func NewStrategyHandler(configs StrategyConfigService, logger *slog.Logger) *StrategyHandler {
	return &StrategyHandler{
		configs: configs,
		logger:  logger,
	}
}

// listStrategyConfigResponse wraps the list of strategy configs.
type listStrategyConfigResponse struct {
	Configs []domain.StrategyConfig `json:"configs"`
}

// GetConfig returns strategy configuration(s).
// GET /api/strategy/config?name=flash_crash
// If name is provided, returns a single config; otherwise returns all configs.
func (h *StrategyHandler) GetConfig(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")

	if name != "" {
		cfg, err := h.configs.Get(r.Context(), name)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				writeError(w, http.StatusNotFound, "strategy config not found")
				return
			}
			h.logger.ErrorContext(r.Context(), "handler: get strategy config failed",
				slog.String("name", name),
				slog.String("error", err.Error()),
			)
			writeError(w, http.StatusInternalServerError, "failed to get strategy config")
			return
		}
		writeJSON(w, http.StatusOK, cfg)
		return
	}

	configs, err := h.configs.List(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: list strategy configs failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to list strategy configs")
		return
	}

	if configs == nil {
		configs = []domain.StrategyConfig{}
	}

	writeJSON(w, http.StatusOK, listStrategyConfigResponse{Configs: configs})
}

// UpdateConfig upserts a strategy configuration.
// PUT /api/strategy/config
func (h *StrategyHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	var cfg domain.StrategyConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if cfg.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	if err := h.configs.Upsert(r.Context(), cfg); err != nil {
		h.logger.ErrorContext(r.Context(), "handler: upsert strategy config failed",
			slog.String("name", cfg.Name),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to update strategy config")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "updated",
		"name":   cfg.Name,
	})
}
