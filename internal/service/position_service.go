package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// PositionService manages trading positions including opening, price updates,
// closing, and stop-loss / take-profit monitoring.
type PositionService struct {
	positions domain.PositionStore
	prices    domain.PriceCache
	bus       domain.SignalBus
	audit     domain.AuditStore
	logger    *slog.Logger
}

// NewPositionService creates a PositionService with all required dependencies.
func NewPositionService(
	positions domain.PositionStore,
	prices domain.PriceCache,
	bus domain.SignalBus,
	audit domain.AuditStore,
	logger *slog.Logger,
) *PositionService {
	return &PositionService{
		positions: positions,
		prices:    prices,
		bus:       bus,
		audit:     audit,
		logger:    logger,
	}
}

// OpenPosition creates a new position from a filled order and the fill price.
func (s *PositionService) OpenPosition(ctx context.Context, order domain.Order, fillPrice float64) (domain.Position, error) {
	now := time.Now().UTC()

	pos := domain.Position{
		ID:            order.ID, // use order ID as position ID
		MarketID:      order.MarketID,
		TokenID:       order.TokenID,
		Wallet:        order.Wallet,
		Side:          "token1",
		Direction:     order.Side,
		EntryPrice:    fillPrice,
		CurrentPrice:  fillPrice,
		Size:          order.Size(),
		UnrealizedPnL: 0,
		RealizedPnL:   0,
		Status:        domain.PositionStatusOpen,
		Strategy:      order.Strategy,
		OpenedAt:      now,
	}

	if err := s.positions.Create(ctx, pos); err != nil {
		return domain.Position{}, fmt.Errorf("position_service: create position: %w", err)
	}

	// Publish position opened event.
	evt, _ := json.Marshal(map[string]any{
		"event":       "position_opened",
		"position_id": pos.ID,
		"market":      pos.MarketID,
		"direction":   string(pos.Direction),
		"entry_price": pos.EntryPrice,
		"size":        pos.Size,
	})
	if pubErr := s.bus.Publish(ctx, "positions", evt); pubErr != nil {
		s.logger.WarnContext(ctx, "position_service: publish event failed",
			slog.String("position_id", pos.ID),
			slog.String("error", pubErr.Error()),
		)
	}

	// Audit log.
	if auditErr := s.audit.Log(ctx, "position_opened", map[string]any{
		"position_id": pos.ID,
		"market":      pos.MarketID,
		"direction":   string(pos.Direction),
		"entry_price": pos.EntryPrice,
		"size":        pos.Size,
		"strategy":    pos.Strategy,
	}); auditErr != nil {
		s.logger.WarnContext(ctx, "position_service: audit log failed",
			slog.String("position_id", pos.ID),
			slog.String("error", auditErr.Error()),
		)
	}

	s.logger.InfoContext(ctx, "position_service: position opened",
		slog.String("position_id", pos.ID),
		slog.String("market", pos.MarketID),
		slog.Float64("entry_price", pos.EntryPrice),
		slog.Float64("size", pos.Size),
	)

	return pos, nil
}

// UpdatePrice updates the current price and unrealized PnL for a position.
func (s *PositionService) UpdatePrice(ctx context.Context, posID string, currentPrice float64) error {
	pos, err := s.positions.GetByID(ctx, posID)
	if err != nil {
		return fmt.Errorf("position_service: get position %q: %w", posID, err)
	}

	pos.CurrentPrice = currentPrice

	// Compute unrealized PnL based on direction.
	switch pos.Direction {
	case domain.OrderSideBuy:
		pos.UnrealizedPnL = (currentPrice - pos.EntryPrice) * pos.Size
	case domain.OrderSideSell:
		pos.UnrealizedPnL = (pos.EntryPrice - currentPrice) * pos.Size
	}

	if err := s.positions.Update(ctx, pos); err != nil {
		return fmt.Errorf("position_service: update position %q: %w", posID, err)
	}

	return nil
}

// ClosePosition closes a position at the given exit price, computes realized
// PnL, and publishes a closure event.
func (s *PositionService) ClosePosition(ctx context.Context, posID string, exitPrice float64) error {
	pos, err := s.positions.GetByID(ctx, posID)
	if err != nil {
		return fmt.Errorf("position_service: get position %q: %w", posID, err)
	}

	// Compute realized PnL.
	var realizedPnL float64
	switch pos.Direction {
	case domain.OrderSideBuy:
		realizedPnL = (exitPrice - pos.EntryPrice) * pos.Size
	case domain.OrderSideSell:
		realizedPnL = (pos.EntryPrice - exitPrice) * pos.Size
	}

	if err := s.positions.Close(ctx, posID, exitPrice); err != nil {
		return fmt.Errorf("position_service: close position %q: %w", posID, err)
	}

	// Publish position closed event.
	evt, _ := json.Marshal(map[string]any{
		"event":        "position_closed",
		"position_id":  posID,
		"market":       pos.MarketID,
		"exit_price":   exitPrice,
		"realized_pnl": realizedPnL,
	})
	if pubErr := s.bus.Publish(ctx, "positions", evt); pubErr != nil {
		s.logger.WarnContext(ctx, "position_service: publish close event failed",
			slog.String("position_id", posID),
			slog.String("error", pubErr.Error()),
		)
	}

	// Audit log.
	if auditErr := s.audit.Log(ctx, "position_closed", map[string]any{
		"position_id":  posID,
		"market":       pos.MarketID,
		"exit_price":   exitPrice,
		"entry_price":  pos.EntryPrice,
		"realized_pnl": realizedPnL,
		"strategy":     pos.Strategy,
	}); auditErr != nil {
		s.logger.WarnContext(ctx, "position_service: audit log failed",
			slog.String("position_id", posID),
			slog.String("error", auditErr.Error()),
		)
	}

	s.logger.InfoContext(ctx, "position_service: position closed",
		slog.String("position_id", posID),
		slog.Float64("exit_price", exitPrice),
		slog.Float64("realized_pnl", realizedPnL),
	)

	return nil
}

// GetOpen returns all open positions for the given wallet.
func (s *PositionService) GetOpen(ctx context.Context, wallet string) ([]domain.Position, error) {
	positions, err := s.positions.GetOpen(ctx, wallet)
	if err != nil {
		return nil, fmt.Errorf("position_service: get open for %q: %w", wallet, err)
	}
	return positions, nil
}

// CheckStopLoss returns open positions whose current price has breached
// the configured stop-loss level.
func (s *PositionService) CheckStopLoss(ctx context.Context, wallet string) ([]domain.Position, error) {
	openPositions, err := s.positions.GetOpen(ctx, wallet)
	if err != nil {
		return nil, fmt.Errorf("position_service: get open for stop-loss check: %w", err)
	}

	var triggered []domain.Position
	for _, pos := range openPositions {
		if pos.StopLoss == nil {
			continue
		}

		// Refresh the current price from cache.
		price, _, priceErr := s.prices.GetPrice(ctx, pos.TokenID)
		if priceErr != nil {
			s.logger.WarnContext(ctx, "position_service: price fetch failed for stop-loss check",
				slog.String("position_id", pos.ID),
				slog.String("token_id", pos.TokenID),
				slog.String("error", priceErr.Error()),
			)
			continue
		}

		sl := *pos.StopLoss
		switch pos.Direction {
		case domain.OrderSideBuy:
			// Long position: stop-loss triggers when price falls to or below SL.
			if price <= sl {
				pos.CurrentPrice = price
				triggered = append(triggered, pos)
			}
		case domain.OrderSideSell:
			// Short position: stop-loss triggers when price rises to or above SL.
			if price >= sl {
				pos.CurrentPrice = price
				triggered = append(triggered, pos)
			}
		}
	}

	return triggered, nil
}

// CheckTakeProfit returns open positions whose current price has reached
// the configured take-profit level.
func (s *PositionService) CheckTakeProfit(ctx context.Context, wallet string) ([]domain.Position, error) {
	openPositions, err := s.positions.GetOpen(ctx, wallet)
	if err != nil {
		return nil, fmt.Errorf("position_service: get open for take-profit check: %w", err)
	}

	var triggered []domain.Position
	for _, pos := range openPositions {
		if pos.TakeProfit == nil {
			continue
		}

		// Refresh the current price from cache.
		price, _, priceErr := s.prices.GetPrice(ctx, pos.TokenID)
		if priceErr != nil {
			s.logger.WarnContext(ctx, "position_service: price fetch failed for take-profit check",
				slog.String("position_id", pos.ID),
				slog.String("token_id", pos.TokenID),
				slog.String("error", priceErr.Error()),
			)
			continue
		}

		tp := *pos.TakeProfit
		switch pos.Direction {
		case domain.OrderSideBuy:
			// Long position: take-profit triggers when price rises to or above TP.
			if price >= tp {
				pos.CurrentPrice = price
				triggered = append(triggered, pos)
			}
		case domain.OrderSideSell:
			// Short position: take-profit triggers when price falls to or below TP.
			if price <= tp {
				pos.CurrentPrice = price
				triggered = append(triggered, pos)
			}
		}
	}

	return triggered, nil
}
