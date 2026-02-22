package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/platform/polymarket"
)

// BondTracker tracks high-probability bond positions from entry to resolution:
// polls Gamma for market resolution, updates BondPosition status and PnL, publishes events.
type BondTracker struct {
	bonds   domain.BondPositionStore
	gamma   *polymarket.GammaClient
	bus     domain.SignalBus
	pollDur time.Duration
	logger  *slog.Logger
}

// NewBondTracker creates a BondTracker. pollInterval is how often to check open positions for resolution.
func NewBondTracker(
	bonds domain.BondPositionStore,
	gamma *polymarket.GammaClient,
	bus domain.SignalBus,
	pollInterval time.Duration,
	logger *slog.Logger,
) *BondTracker {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Minute
	}
	return &BondTracker{
		bonds:   bonds,
		gamma:   gamma,
		bus:     bus,
		pollDur: pollInterval,
		logger:  logger.With(slog.String("component", "bond_tracker")),
	}
}

// Run polls open bond positions and updates status on resolution. Call in a goroutine.
func (b *BondTracker) Run(ctx context.Context) error {
	ticker := time.NewTicker(b.pollDur)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := b.checkResolutions(ctx); err != nil {
				b.logger.ErrorContext(ctx, "bond tracker check resolutions failed", slog.String("error", err.Error()))
			}
		}
	}
}

func (b *BondTracker) checkResolutions(ctx context.Context) error {
	open, err := b.bonds.GetOpen(ctx)
	if err != nil {
		return err
	}
	for _, pos := range open {
		if pos.Status != domain.BondOpen {
			continue
		}
		res, err := b.gamma.GetMarketResolution(ctx, pos.MarketID)
		if err != nil {
			b.logger.DebugContext(ctx, "bond resolution fetch failed", slog.String("market_id", pos.MarketID), slog.String("error", err.Error()))
			continue
		}
		if !res.Closed {
			continue
		}
		now := time.Now().UTC()
		pos.ResolvedAt = &now
		if res.YesWon {
			pos.Status = domain.BondResolvedWin
			pos.RealizedPnL = pos.Size*(1.0-pos.EntryPrice) - pos.EntryPrice*0 // payout 1 per share, cost entry
		} else {
			pos.Status = domain.BondResolvedLoss
			pos.RealizedPnL = -pos.Size * pos.EntryPrice
		}
		if err := b.bonds.Update(ctx, pos); err != nil {
			b.logger.ErrorContext(ctx, "bond position update failed", slog.String("id", pos.ID), slog.String("error", err.Error()))
			continue
		}
		b.logger.InfoContext(ctx, "bond position resolved",
			slog.String("id", pos.ID),
			slog.String("market_id", pos.MarketID),
			slog.String("status", string(pos.Status)),
			slog.Float64("realized_pnl", pos.RealizedPnL),
		)
		if b.bus != nil {
			payload, _ := json.Marshal(map[string]any{
				"event":        "bond_resolved",
				"position_id":  pos.ID,
				"market_id":    pos.MarketID,
				"status":       string(pos.Status),
				"realized_pnl": pos.RealizedPnL,
			})
			_ = b.bus.Publish(ctx, "bond_resolved", payload)
		}
	}
	return nil
}

// PortfolioAPR returns the aggregate expected APR from open bond positions (simple average of ExpectedAPR).
func (b *BondTracker) PortfolioAPR(ctx context.Context) (float64, error) {
	open, err := b.bonds.GetOpen(ctx)
	if err != nil {
		return 0, err
	}
	if len(open) == 0 {
		return 0, nil
	}
	var sum float64
	for _, pos := range open {
		sum += pos.ExpectedAPR
	}
	return sum / float64(len(open)), nil
}
