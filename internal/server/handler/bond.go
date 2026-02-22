package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// BondService defines the methods that the bond handler requires.
type BondService interface {
	GetOpen(ctx context.Context) ([]domain.BondPosition, error)
	GetByID(ctx context.Context, id string) (domain.BondPosition, error)
}

// BondHandler serves bond-position-related HTTP endpoints.
type BondHandler struct {
	bonds  BondService
	logger *slog.Logger
}

// NewBondHandler creates a BondHandler with the given service and logger.
func NewBondHandler(bonds BondService, logger *slog.Logger) *BondHandler {
	return &BondHandler{bonds: bonds, logger: logger}
}

// ListBonds returns all open bond positions.
// GET /api/bonds
func (h *BondHandler) ListBonds(w http.ResponseWriter, r *http.Request) {
	positions, err := h.bonds.GetOpen(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: list bonds failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to list bond positions")
		return
	}

	if positions == nil {
		positions = []domain.BondPosition{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"bonds": positions,
		"count": len(positions),
	})
}

// GetBond returns a single bond position by ID.
// GET /api/bonds/{id}
func (h *BondHandler) GetBond(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing bond id")
		return
	}

	bond, err := h.bonds.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "bond position not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "handler: get bond failed",
			slog.String("bond_id", id),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to get bond position")
		return
	}

	writeJSON(w, http.StatusOK, bond)
}

// Summary returns a portfolio summary of open bond positions.
// GET /api/bonds/summary
func (h *BondHandler) Summary(w http.ResponseWriter, r *http.Request) {
	positions, err := h.bonds.GetOpen(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: bond summary failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to compute bond summary")
		return
	}

	var totalInvested, totalExpectedYield, weightedAPRNum float64
	for _, p := range positions {
		invested := p.EntryPrice * p.Size
		totalInvested += invested

		expectedYield := (1.0 - p.EntryPrice) * p.Size
		totalExpectedYield += expectedYield

		weightedAPRNum += p.ExpectedAPR * invested
	}

	weightedAPR := 0.0
	if totalInvested > 0 {
		weightedAPR = weightedAPRNum / totalInvested
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"open_count":     len(positions),
		"total_invested": totalInvested,
		"expected_yield": totalExpectedYield,
		"weighted_apr":   weightedAPR,
	})
}
