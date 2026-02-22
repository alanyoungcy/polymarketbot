package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ArbService defines the methods that the arbitrage handler requires.
type ArbService interface {
	ListRecent(ctx context.Context, limit int) ([]domain.ArbOpportunity, error)
}

// ArbExecutionStore is used for profit and executions endpoints.
type ArbExecutionStore interface {
	SumPnL(ctx context.Context, since time.Time) (float64, error)
	SumPnLByType(ctx context.Context, arbType domain.ArbType, since time.Time) (float64, error)
	ListRecent(ctx context.Context, limit int) ([]domain.ArbExecution, error)
	GetByID(ctx context.Context, id string) (domain.ArbExecution, error)
}

// ArbHandler serves arbitrage-related HTTP endpoints.
type ArbHandler struct {
	arb     ArbService
	arbExec ArbExecutionStore // optional; when nil, Profit and Executions return 501
	logger  *slog.Logger
}

// NewArbHandler creates an ArbHandler with the given service and logger.
func NewArbHandler(arb ArbService, logger *slog.Logger) *ArbHandler {
	return &ArbHandler{arb: arb, logger: logger}
}

// WithArbExecutionStore sets the execution store for profit/executions endpoints.
func (h *ArbHandler) WithArbExecutionStore(store ArbExecutionStore) *ArbHandler {
	h.arbExec = store
	return h
}

// listArbResponse wraps the list arbitrage opportunities response.
type listArbResponse struct {
	Opportunities []domain.ArbOpportunity `json:"opportunities"`
}

// ListRecent returns the most recent arbitrage opportunities.
// GET /api/arbitrage/recent?limit=20
func (h *ArbHandler) ListRecent(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}

	opps, err := h.arb.ListRecent(r.Context(), limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: list arb opportunities failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to list arbitrage opportunities")
		return
	}

	if opps == nil {
		opps = []domain.ArbOpportunity{}
	}

	writeJSON(w, http.StatusOK, listArbResponse{Opportunities: opps})
}

// Profit returns session or filtered PnL summary.
// GET /api/arbitrage/profit?type=rebalancing&since=2025-01-01
func (h *ArbHandler) Profit(w http.ResponseWriter, r *http.Request) {
	if h.arbExec == nil {
		writeError(w, http.StatusNotImplemented, "arbitrage execution tracking not configured")
		return
	}
	since := time.Now().UTC().Add(-24 * time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, time.UTC); err == nil {
			since = t
		}
	}
	var totalPnL float64
	var err error
	if arbType := r.URL.Query().Get("type"); arbType != "" {
		t := domain.ArbType(arbType)
		totalPnL, err = h.arbExec.SumPnLByType(r.Context(), t, since)
	} else {
		totalPnL, err = h.arbExec.SumPnL(r.Context(), since)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: arb profit failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to compute profit")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"since":       since.Format(time.RFC3339),
		"total_pnl_usd": totalPnL,
	})
}

// ListExecutions returns recent arb executions with legs.
// GET /api/arbitrage/executions?limit=50
func (h *ArbHandler) ListExecutions(w http.ResponseWriter, r *http.Request) {
	if h.arbExec == nil {
		writeError(w, http.StatusNotImplemented, "arbitrage execution tracking not configured")
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	list, err := h.arbExec.ListRecent(r.Context(), limit)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: list arb executions failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to list executions")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"executions": list})
}

// GetExecution returns a single execution by id.
// GET /api/arbitrage/executions/{id}
func (h *ArbHandler) GetExecution(w http.ResponseWriter, r *http.Request) {
	if h.arbExec == nil {
		writeError(w, http.StatusNotImplemented, "arbitrage execution tracking not configured")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing execution id")
		return
	}
	exec, err := h.arbExec.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "execution not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "handler: get arb execution failed", slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "failed to get execution")
		return
	}
	writeJSON(w, http.StatusOK, exec)
}
