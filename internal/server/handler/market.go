package handler

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// MarketService defines the methods that the market handler requires from the
// service layer. It is declared locally so the handler package does not depend
// on the concrete service implementation.
type MarketService interface {
	GetMarket(ctx context.Context, id string) (domain.Market, error)
	ListActive(ctx context.Context, opts domain.ListOpts) ([]domain.Market, error)
	Count(ctx context.Context) (int64, error)
}

// MarketHandler serves market-related HTTP endpoints.
type MarketHandler struct {
	markets MarketService
	logger  *slog.Logger
}

// NewMarketHandler creates a MarketHandler with the given service and logger.
func NewMarketHandler(markets MarketService, logger *slog.Logger) *MarketHandler {
	return &MarketHandler{
		markets: markets,
		logger:  logger,
	}
}

// listMarketsResponse wraps the list endpoint output with metadata.
type listMarketsResponse struct {
	Markets []domain.Market `json:"markets"`
	Total   int64           `json:"total"`
	Limit   int             `json:"limit"`
	Offset  int             `json:"offset"`
}

// ListMarkets returns active markets with pagination.
// GET /api/markets?limit=50&offset=0
func (h *MarketHandler) ListMarkets(w http.ResponseWriter, r *http.Request) {
	opts := parseListOpts(r)

	markets, err := h.markets.ListActive(r.Context(), opts)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: list markets failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to list markets")
		return
	}

	total, err := h.markets.Count(r.Context())
	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: count markets failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to count markets")
		return
	}

	writeJSON(w, http.StatusOK, listMarketsResponse{
		Markets: markets,
		Total:   total,
		Limit:   opts.Limit,
		Offset:  opts.Offset,
	})
}

// GetMarket returns a single market by its ID.
// GET /api/markets/{id}
func (h *MarketHandler) GetMarket(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing market id")
		return
	}

	market, err := h.markets.GetMarket(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "market not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "handler: get market failed",
			slog.String("market_id", id),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to get market")
		return
	}

	writeJSON(w, http.StatusOK, market)
}
