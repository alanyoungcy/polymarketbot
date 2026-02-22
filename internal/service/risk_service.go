package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// RiskConfig holds the tunable parameters for pre-trade risk checks.
type RiskConfig struct {
	MaxPositions   int
	MaxTradeAmount float64
	MaxSlippageBps float64
}

// RiskService provides pre-trade risk checks to ensure orders stay within
// configured risk limits before being submitted.
type RiskService struct {
	positions domain.PositionStore
	prices    domain.PriceCache
	cfg       RiskConfig
	logger    *slog.Logger
}

// NewRiskService creates a RiskService with all required dependencies.
func NewRiskService(
	positions domain.PositionStore,
	prices domain.PriceCache,
	cfg RiskConfig,
	logger *slog.Logger,
) *RiskService {
	return &RiskService{
		positions: positions,
		prices:    prices,
		cfg:       cfg,
		logger:    logger,
	}
}

// PreTradeCheck validates a trade signal against the configured risk limits
// for the given wallet. It returns a non-nil error describing the first
// failed check, or nil if all checks pass.
//
// Checks performed:
//  1. Maximum number of open positions
//  2. Trade size within limits
//  3. Estimated slippage within bounds
func (s *RiskService) PreTradeCheck(ctx context.Context, signal domain.TradeSignal, wallet string) error {
	// Check 1: max open positions.
	openPositions, err := s.positions.GetOpen(ctx, wallet)
	if err != nil {
		return fmt.Errorf("risk_service: get open positions: %w", err)
	}
	if len(openPositions) >= s.cfg.MaxPositions {
		s.logger.WarnContext(ctx, "risk_service: max positions reached",
			slog.String("wallet", wallet),
			slog.Int("open", len(openPositions)),
			slog.Int("max", s.cfg.MaxPositions),
		)
		return fmt.Errorf("risk_service: max positions reached (%d/%d)", len(openPositions), s.cfg.MaxPositions)
	}

	// Check 2: trade size within limits.
	tradeAmount := signal.Price() * signal.Size()
	if tradeAmount > s.cfg.MaxTradeAmount {
		s.logger.WarnContext(ctx, "risk_service: trade amount exceeds limit",
			slog.String("wallet", wallet),
			slog.Float64("amount", tradeAmount),
			slog.Float64("max", s.cfg.MaxTradeAmount),
		)
		return fmt.Errorf("risk_service: trade amount %.2f exceeds max %.2f", tradeAmount, s.cfg.MaxTradeAmount)
	}

	// Check 3: slippage bounds.
	currentPrice, _, priceErr := s.prices.GetPrice(ctx, signal.TokenID)
	if priceErr != nil {
		// If we cannot fetch the current price, we cannot estimate slippage.
		// Log a warning but do not block the trade.
		s.logger.WarnContext(ctx, "risk_service: could not fetch price for slippage check",
			slog.String("token_id", signal.TokenID),
			slog.String("error", priceErr.Error()),
		)
		return nil
	}

	if currentPrice > 0 {
		signalPrice := signal.Price()
		var slippageBps float64
		switch signal.Side {
		case domain.OrderSideBuy:
			// For buys, slippage is how much more we pay vs. current price.
			slippageBps = ((signalPrice - currentPrice) / currentPrice) * 10_000
		case domain.OrderSideSell:
			// For sells, slippage is how much less we receive vs. current price.
			slippageBps = ((currentPrice - signalPrice) / currentPrice) * 10_000
		}

		if slippageBps > s.cfg.MaxSlippageBps {
			s.logger.WarnContext(ctx, "risk_service: slippage exceeds limit",
				slog.String("wallet", wallet),
				slog.Float64("slippage_bps", slippageBps),
				slog.Float64("max_slippage_bps", s.cfg.MaxSlippageBps),
			)
			return fmt.Errorf("risk_service: slippage %.1f bps exceeds max %.1f bps", slippageBps, s.cfg.MaxSlippageBps)
		}
	}

	return nil
}

// PositionExposure computes the total notional exposure across all open
// positions for the given wallet. Notional is calculated as
// current_price * size for each open position.
func (s *RiskService) PositionExposure(ctx context.Context, wallet string) (float64, error) {
	openPositions, err := s.positions.GetOpen(ctx, wallet)
	if err != nil {
		return 0, fmt.Errorf("risk_service: get open positions: %w", err)
	}

	// Collect all token IDs for a batch price lookup.
	tokenIDs := make([]string, 0, len(openPositions))
	for _, p := range openPositions {
		tokenIDs = append(tokenIDs, p.TokenID)
	}

	prices, priceErr := s.prices.GetPrices(ctx, tokenIDs)
	if priceErr != nil {
		return 0, fmt.Errorf("risk_service: get prices for exposure: %w", priceErr)
	}

	var totalExposure float64
	for _, p := range openPositions {
		price, ok := prices[p.TokenID]
		if !ok {
			// Fall back to the stored current price if cache miss.
			price = p.CurrentPrice
		}
		totalExposure += price * p.Size
	}

	return totalExposure, nil
}
