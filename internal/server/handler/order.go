package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// OrderService defines the methods that the order handler requires from the
// service layer.
type OrderService interface {
	PlaceOrder(ctx context.Context, sig domain.TradeSignal) (domain.OrderResult, error)
	CancelOrder(ctx context.Context, orderID string) error
	ListOpen(ctx context.Context, wallet string) ([]domain.Order, error)
	ListByMarket(ctx context.Context, marketID string, opts domain.ListOpts) ([]domain.Order, error)
}

// OrderHandler serves order-related HTTP endpoints.
type OrderHandler struct {
	orders OrderService
	logger *slog.Logger
}

// NewOrderHandler creates an OrderHandler with the given service and logger.
func NewOrderHandler(orders OrderService, logger *slog.Logger) *OrderHandler {
	return &OrderHandler{
		orders: orders,
		logger: logger,
	}
}

// listOrdersResponse wraps the list orders response.
type listOrdersResponse struct {
	Orders []domain.Order `json:"orders"`
}

// ListOrders returns open orders for a wallet, or orders for a specific market.
// GET /api/orders?wallet=0x...&market_id=...&limit=50&offset=0
func (h *OrderHandler) ListOrders(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wallet := q.Get("wallet")
	marketID := q.Get("market_id")

	if wallet == "" && marketID == "" {
		writeError(w, http.StatusBadRequest, "wallet or market_id query parameter required")
		return
	}

	var orders []domain.Order
	var err error

	if marketID != "" {
		opts := parseListOpts(r)
		orders, err = h.orders.ListByMarket(r.Context(), marketID, opts)
	} else {
		orders, err = h.orders.ListOpen(r.Context(), wallet)
	}

	if err != nil {
		h.logger.ErrorContext(r.Context(), "handler: list orders failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to list orders")
		return
	}

	if orders == nil {
		orders = []domain.Order{}
	}

	writeJSON(w, http.StatusOK, listOrdersResponse{Orders: orders})
}

// PlaceOrder creates a new order from a trade signal JSON body.
// POST /api/orders
func (h *OrderHandler) PlaceOrder(w http.ResponseWriter, r *http.Request) {
	var sig domain.TradeSignal
	if err := json.NewDecoder(r.Body).Decode(&sig); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if sig.MarketID == "" || sig.TokenID == "" {
		writeError(w, http.StatusBadRequest, "market_id and token_id are required")
		return
	}

	result, err := h.orders.PlaceOrder(r.Context(), sig)
	if err != nil {
		if errors.Is(err, domain.ErrRateLimited) {
			writeError(w, http.StatusTooManyRequests, "rate limited")
			return
		}
		if errors.Is(err, domain.ErrInvalidOrder) {
			writeError(w, http.StatusBadRequest, result.Message)
			return
		}
		h.logger.ErrorContext(r.Context(), "handler: place order failed",
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to place order")
		return
	}

	writeJSON(w, http.StatusCreated, result)
}

// CancelOrder cancels an existing order by its ID.
// DELETE /api/orders/{id}
func (h *OrderHandler) CancelOrder(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing order id")
		return
	}

	if err := h.orders.CancelOrder(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "order not found")
			return
		}
		h.logger.ErrorContext(r.Context(), "handler: cancel order failed",
			slog.String("order_id", id),
			slog.String("error", err.Error()),
		)
		writeError(w, http.StatusInternalServerError, "failed to cancel order")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "cancelled",
		"order_id": id,
	})
}
