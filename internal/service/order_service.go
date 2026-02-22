package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/crypto"
	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/ethereum/go-ethereum/common"
)

// Signer abstracts EIP-712 order signing so the service layer never depends
// on concrete key-management implementations.
type Signer interface {
	SignOrder(payload crypto.OrderPayload) (string, error)
	Address() common.Address
}

// ClobPoster submits signed orders to the Polymarket CLOB API.
type ClobPoster interface {
	PostOrder(ctx context.Context, order domain.Order) (domain.OrderResult, error)
}

// OrderService handles the order lifecycle from signal to confirmed order.
type OrderService struct {
	orders     domain.OrderStore
	positions  domain.PositionStore
	book       domain.OrderbookCache
	prices     domain.PriceCache
	limiter    domain.RateLimiter
	bus        domain.SignalBus
	audit      domain.AuditStore
	signer     Signer
	clobClient ClobPoster
	logger     *slog.Logger
}

// NewOrderService creates an OrderService with all required dependencies.
func NewOrderService(
	orders domain.OrderStore,
	positions domain.PositionStore,
	book domain.OrderbookCache,
	prices domain.PriceCache,
	limiter domain.RateLimiter,
	bus domain.SignalBus,
	audit domain.AuditStore,
	signer Signer,
	logger *slog.Logger,
) *OrderService {
	return &OrderService{
		orders:    orders,
		positions: positions,
		book:      book,
		prices:    prices,
		limiter:   limiter,
		bus:       bus,
		audit:     audit,
		signer:    signer,
		logger:    logger,
	}
}

// WithClobClient attaches a CLOB poster so PlaceOrder submits orders to the
// exchange after persisting locally. Without a CLOB client, PlaceOrder works
// in local-only mode (useful for testing/paper trading).
func (s *OrderService) WithClobClient(poster ClobPoster) *OrderService {
	s.clobClient = poster
	return s
}

// PlaceOrder converts a TradeSignal into a signed order, persists it, publishes
// an event on the signal bus, and writes an audit log entry.
func (s *OrderService) PlaceOrder(ctx context.Context, sig domain.TradeSignal) (domain.OrderResult, error) {
	// Rate limit check.
	allowed, err := s.limiter.Allow(ctx, "orders:"+s.signer.Address().Hex(), 10, time.Second)
	if err != nil {
		return domain.OrderResult{}, fmt.Errorf("order_service: rate limiter: %w", err)
	}
	if !allowed {
		return domain.OrderResult{
			Success:     false,
			Message:     "rate limited",
			ShouldRetry: true,
		}, domain.ErrRateLimited
	}

	// Build the order from the signal.
	wallet := s.signer.Address().Hex()

	order := domain.Order{
		ID:       sig.ID,
		MarketID: sig.MarketID,
		TokenID:  sig.TokenID,
		Wallet:   wallet,
		Side:     sig.Side,
		Type:     domain.OrderTypeGTC,
		PriceTicks: sig.PriceTicks,
		SizeUnits:  sig.SizeUnits,
		Status:     domain.OrderStatusPending,
		Strategy:   sig.Source,
		CreatedAt:  time.Now().UTC(),
	}

	// Build the signing payload.
	sideInt := 0
	if sig.Side == domain.OrderSideSell {
		sideInt = 1
	}

	payload := crypto.OrderPayload{
		Salt:          fmt.Sprintf("%d", time.Now().UnixNano()),
		Maker:         wallet,
		Signer:        wallet,
		Taker:         "0x0000000000000000000000000000000000000000",
		TokenID:       sig.TokenID,
		MakerAmount:   fmt.Sprintf("%d", sig.PriceTicks),
		TakerAmount:   fmt.Sprintf("%d", sig.SizeUnits),
		Expiration:    "0",
		Nonce:         "0",
		FeeRateBps:    "0",
		Side:          sideInt,
		SignatureType: 0,
	}

	signature, err := s.signer.SignOrder(payload)
	if err != nil {
		return domain.OrderResult{
			Success: false,
			Message: "signing failed",
		}, fmt.Errorf("order_service: sign order: %w", err)
	}
	order.Signature = signature

	// Persist the order.
	if err := s.orders.Create(ctx, order); err != nil {
		return domain.OrderResult{
			Success: false,
			Message: "persist failed",
		}, fmt.Errorf("order_service: create order: %w", err)
	}

	// Submit to CLOB if a poster is configured.
	if s.clobClient != nil {
		clobResult, clobErr := s.clobClient.PostOrder(ctx, order)
		if clobErr != nil {
			_ = s.orders.UpdateStatus(ctx, order.ID, domain.OrderStatusFailed)
			return domain.OrderResult{
				Success: false,
				OrderID: order.ID,
				Message: clobErr.Error(),
			}, fmt.Errorf("order_service: clob post order: %w", clobErr)
		}
		// Update local order status based on CLOB response.
		if clobResult.Status != "" {
			_ = s.orders.UpdateStatus(ctx, order.ID, clobResult.Status)
		}
		if clobResult.OrderID == "" {
			clobResult.OrderID = order.ID
		}

		// Publish order placed event.
		evt, _ := json.Marshal(map[string]string{
			"event":    "order_placed",
			"order_id": clobResult.OrderID,
			"market":   order.MarketID,
			"side":     string(order.Side),
			"status":   string(clobResult.Status),
		})
		if pubErr := s.bus.Publish(ctx, "orders", evt); pubErr != nil {
			s.logger.WarnContext(ctx, "order_service: publish event failed",
				slog.String("order_id", clobResult.OrderID),
				slog.String("error", pubErr.Error()),
			)
		}

		// Audit log.
		if auditErr := s.audit.Log(ctx, "order_placed", map[string]any{
			"order_id": clobResult.OrderID,
			"market":   order.MarketID,
			"side":     string(order.Side),
			"price":    order.Price(),
			"size":     order.Size(),
			"strategy": order.Strategy,
			"clob":     true,
		}); auditErr != nil {
			s.logger.WarnContext(ctx, "order_service: audit log failed",
				slog.String("order_id", clobResult.OrderID),
				slog.String("error", auditErr.Error()),
			)
		}

		s.logger.InfoContext(ctx, "order_service: order placed via CLOB",
			slog.String("order_id", clobResult.OrderID),
			slog.String("market", order.MarketID),
			slog.String("side", string(order.Side)),
			slog.String("status", string(clobResult.Status)),
		)

		return clobResult, nil
	}

	// Publish order placed event.
	evt, _ := json.Marshal(map[string]string{
		"event":    "order_placed",
		"order_id": order.ID,
		"market":   order.MarketID,
		"side":     string(order.Side),
	})
	if pubErr := s.bus.Publish(ctx, "orders", evt); pubErr != nil {
		s.logger.WarnContext(ctx, "order_service: publish event failed",
			slog.String("order_id", order.ID),
			slog.String("error", pubErr.Error()),
		)
	}

	// Audit log.
	if auditErr := s.audit.Log(ctx, "order_placed", map[string]any{
		"order_id": order.ID,
		"market":   order.MarketID,
		"side":     string(order.Side),
		"price":    order.Price(),
		"size":     order.Size(),
		"strategy": order.Strategy,
	}); auditErr != nil {
		s.logger.WarnContext(ctx, "order_service: audit log failed",
			slog.String("order_id", order.ID),
			slog.String("error", auditErr.Error()),
		)
	}

	s.logger.InfoContext(ctx, "order_service: order placed",
		slog.String("order_id", order.ID),
		slog.String("market", order.MarketID),
		slog.String("side", string(order.Side)),
	)

	return domain.OrderResult{
		Success: true,
		OrderID: order.ID,
		Status:  domain.OrderStatusPending,
		Message: "order placed",
	}, nil
}

// CancelOrder cancels a single order by updating its status and publishing
// a cancellation event.
func (s *OrderService) CancelOrder(ctx context.Context, orderID string) error {
	if err := s.orders.UpdateStatus(ctx, orderID, domain.OrderStatusCancelled); err != nil {
		return fmt.Errorf("order_service: cancel order %q: %w", orderID, err)
	}

	// Publish cancellation event.
	evt, _ := json.Marshal(map[string]string{
		"event":    "order_cancelled",
		"order_id": orderID,
	})
	if pubErr := s.bus.Publish(ctx, "orders", evt); pubErr != nil {
		s.logger.WarnContext(ctx, "order_service: publish cancel event failed",
			slog.String("order_id", orderID),
			slog.String("error", pubErr.Error()),
		)
	}

	// Audit log.
	if auditErr := s.audit.Log(ctx, "order_cancelled", map[string]any{
		"order_id": orderID,
	}); auditErr != nil {
		s.logger.WarnContext(ctx, "order_service: audit log failed",
			slog.String("order_id", orderID),
			slog.String("error", auditErr.Error()),
		)
	}

	s.logger.InfoContext(ctx, "order_service: order cancelled",
		slog.String("order_id", orderID),
	)

	return nil
}

// ReplaceOrder atomically cancels the existing order and places a new one.
// Used by liquidity_provider strategy for requoting.
func (s *OrderService) ReplaceOrder(ctx context.Context, cancelID string, newSig domain.TradeSignal) (domain.OrderResult, error) {
	if err := s.CancelOrder(ctx, cancelID); err != nil {
		return domain.OrderResult{}, fmt.Errorf("order_service: replace order cancel leg failed: %w", err)
	}
	return s.PlaceOrder(ctx, newSig)
}

// CancelAll cancels all open orders for the given wallet address.
func (s *OrderService) CancelAll(ctx context.Context, wallet string) error {
	openOrders, err := s.orders.ListOpen(ctx, wallet)
	if err != nil {
		return fmt.Errorf("order_service: list open orders for %q: %w", wallet, err)
	}

	var firstErr error
	for _, o := range openOrders {
		if cancelErr := s.CancelOrder(ctx, o.ID); cancelErr != nil {
			s.logger.ErrorContext(ctx, "order_service: cancel failed during cancel-all",
				slog.String("order_id", o.ID),
				slog.String("error", cancelErr.Error()),
			)
			if firstErr == nil {
				firstErr = cancelErr
			}
		}
	}

	if firstErr != nil {
		return fmt.Errorf("order_service: cancel all for %q: %w", wallet, firstErr)
	}

	s.logger.InfoContext(ctx, "order_service: cancelled all open orders",
		slog.String("wallet", wallet),
		slog.Int("count", len(openOrders)),
	)

	return nil
}

// GetOrder retrieves a single order by its ID.
func (s *OrderService) GetOrder(ctx context.Context, id string) (domain.Order, error) {
	order, err := s.orders.GetByID(ctx, id)
	if err != nil {
		return domain.Order{}, fmt.Errorf("order_service: get order %q: %w", id, err)
	}
	return order, nil
}

// ListOpen returns all open orders for the given wallet address.
func (s *OrderService) ListOpen(ctx context.Context, wallet string) ([]domain.Order, error) {
	orders, err := s.orders.ListOpen(ctx, wallet)
	if err != nil {
		return nil, fmt.Errorf("order_service: list open for %q: %w", wallet, err)
	}
	return orders, nil
}

// ListByMarket returns orders for a specific market with pagination.
func (s *OrderService) ListByMarket(ctx context.Context, marketID string, opts domain.ListOpts) ([]domain.Order, error) {
	orders, err := s.orders.ListByMarket(ctx, marketID, opts)
	if err != nil {
		return nil, fmt.Errorf("order_service: list by market %q: %w", marketID, err)
	}
	return orders, nil
}
