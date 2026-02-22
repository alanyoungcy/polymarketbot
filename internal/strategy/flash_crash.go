package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

const (
	defaultDropThreshold  = 0.10
	defaultRecoveryTarget = 0.05
)

// FlashCrash implements a strategy that emits BUY signals when the price of an
// asset drops sharply relative to its recent average. The idea is to capture
// transient liquidity dislocations where the price is expected to recover.
type FlashCrash struct {
	cfg     Config
	tracker *PriceTracker
	logger  *slog.Logger
}

// NewFlashCrash creates a FlashCrash strategy with the supplied configuration
// and price tracker. The following keys are read from cfg.Params:
//
//   - "drop_threshold" (float64): minimum fractional drop to trigger a signal.
//     Defaults to 0.10 (10 %).
//   - "recovery_target" (float64): expected fractional recovery used to set the
//     signal price above the crash level. Defaults to 0.05 (5 %).
func NewFlashCrash(cfg Config, tracker *PriceTracker, logger *slog.Logger) *FlashCrash {
	return &FlashCrash{
		cfg:     cfg,
		tracker: tracker,
		logger:  logger.With(slog.String("strategy", "flash_crash")),
	}
}

// Name returns the strategy identifier.
func (fc *FlashCrash) Name() string { return "flash_crash" }

// Init performs any one-time setup. For FlashCrash this is a no-op.
func (fc *FlashCrash) Init(_ context.Context) error { return nil }

// OnBookUpdate evaluates the latest orderbook snapshot for a flash crash
// condition and returns a BUY signal if the threshold has been breached.
func (fc *FlashCrash) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	_ = ctx

	assetID := snap.AssetID
	bestBid := snap.BestBid

	// Record the new price observation.
	fc.tracker.Track(assetID, bestBid, snap.Timestamp)

	threshold := fc.dropThreshold()
	if !fc.tracker.DetectFlashCrash(assetID, threshold) {
		return nil, nil
	}

	avg := fc.tracker.GetAverage(assetID)
	recovery := fc.recoveryTarget()

	// Target price: current best bid + a fraction of the distance back to the
	// average, governed by the recovery target.
	targetPrice := bestBid + (avg-bestBid)*recovery

	priceTicks := int64(targetPrice * 1e6)
	sizeUnits := int64(fc.cfg.Size * 1e6)

	now := time.Now().UTC()
	sig := domain.TradeSignal{
		ID:         fmt.Sprintf("fc-%s-%d", assetID, now.UnixNano()),
		Source:     fc.Name(),
		MarketID:   "", // caller must enrich if needed
		TokenID:    assetID,
		Side:       domain.OrderSideBuy,
		PriceTicks: priceTicks,
		SizeUnits:  sizeUnits,
		Urgency:    domain.SignalUrgencyHigh,
		Reason:     fmt.Sprintf("flash crash detected: bid=%.6f avg=%.6f drop=%.2f%%", bestBid, avg, threshold*100),
		Metadata: map[string]string{
			"avg_price":       fmt.Sprintf("%.6f", avg),
			"drop_threshold":  fmt.Sprintf("%.4f", threshold),
			"recovery_target": fmt.Sprintf("%.4f", recovery),
		},
		CreatedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
	}

	fc.logger.Info("flash crash signal emitted",
		slog.String("asset", assetID),
		slog.Float64("best_bid", bestBid),
		slog.Float64("avg", avg),
		slog.Float64("target_price", targetPrice),
	)

	return []domain.TradeSignal{sig}, nil
}

// OnPriceChange tracks the price update but does not generate signals from
// incremental level changes alone.
func (fc *FlashCrash) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	fc.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}

// OnTrade records trade prices in the tracker but does not emit signals.
func (fc *FlashCrash) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	fc.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}

// OnSignal is a no-op for FlashCrash; it does not react to external signals.
func (fc *FlashCrash) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}

// Close releases resources. FlashCrash has nothing to release.
func (fc *FlashCrash) Close() error { return nil }

// dropThreshold returns the configured drop threshold or the default.
func (fc *FlashCrash) dropThreshold() float64 {
	if v, ok := fc.cfg.Params["drop_threshold"]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return defaultDropThreshold
}

// recoveryTarget returns the configured recovery target or the default.
func (fc *FlashCrash) recoveryTarget() float64 {
	if v, ok := fc.cfg.Params["recovery_target"]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return defaultRecoveryTarget
}
