package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// PositionService defines the methods that the position handler requires.
type PositionService interface {
	GetOpen(ctx context.Context, wallet string) ([]domain.Position, error)
}

// PositionHandler serves position-related HTTP endpoints.
type PositionHandler struct {
	positions PositionService
	logger    *slog.Logger
}

// NewPositionHandler creates a PositionHandler with the given service and logger.
func NewPositionHandler(positions PositionService, logger *slog.Logger) *PositionHandler {
	return &PositionHandler{
		positions: positions,
		logger:    logger,
	}
}

// listPositionsResponse wraps the list positions response.
type listPositionsResponse struct {
	Positions []domain.Position `json:"positions"`
}

// ListPositions returns all open positions for a given wallet.
// GET /api/positions?wallet=0x...
func (h *PositionHandler) ListPositions(w http.ResponseWriter, r *http.Request) {
	wallet := r.URL.Query().Get("wallet")
	if wallet == "" {
		writeError(w, http.StatusBadRequest, "wallet query parameter required")
		return
	}

	positions, err := h.positions.GetOpen(r.Context(), wallet)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: list positions failed",
			slog.String("wallet", wallet),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to list positions")
		return
	}

	if positions == nil {
		positions = []domain.Position{}
	}

	writeJSON(w, http.StatusOK, listPositionsResponse{Positions: positions})
}
