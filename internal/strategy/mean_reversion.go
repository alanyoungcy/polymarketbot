package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

const (
	defaultStdDevThreshold  = 2.0
	defaultLookbackWindow   = "5m"
)

// MeanReversion implements a strategy that buys when the current price is
// significantly below the recent mean and sells when it is significantly
// above.  "Significantly" is measured in multiples of the trailing standard
// deviation (the std_dev_threshold parameter).
type MeanReversion struct {
	cfg     Config
	tracker *PriceTracker
	logger  *slog.Logger
}

// NewMeanReversion creates a MeanReversion strategy. The following keys are
// read from cfg.Params:
//
//   - "lookback_window" (string, parseable by time.ParseDuration): controls the
//     PriceTracker window used for mean/volatility calculations.
//     Defaults to "5m".
//   - "std_dev_threshold" (float64): number of standard deviations away from
//     the mean before a signal is emitted. Defaults to 2.0.
func NewMeanReversion(cfg Config, tracker *PriceTracker, logger *slog.Logger) *MeanReversion {
	return &MeanReversion{
		cfg:     cfg,
		tracker: tracker,
		logger:  logger.With(slog.String("strategy", "mean_reversion")),
	}
}

// Name returns the strategy identifier.
func (mr *MeanReversion) Name() string { return "mean_reversion" }

// Init performs one-time setup. For MeanReversion this is a no-op.
func (mr *MeanReversion) Init(_ context.Context) error { return nil }

// OnBookUpdate evaluates whether the current mid price deviates enough from
// the historical average to warrant a buy or sell signal.
func (mr *MeanReversion) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	_ = ctx

	assetID := snap.AssetID
	mid := snap.MidPrice

	// Record the observation.
	mr.tracker.Track(assetID, mid, snap.Timestamp)

	avg := mr.tracker.GetAverage(assetID)
	vol := mr.tracker.GetVolatility(assetID)
	if vol == 0 || avg == 0 {
		// Not enough data yet.
		return nil, nil
	}

	threshold := mr.stdDevThreshold()
	deviation := (mid - avg) / vol

	now := time.Now().UTC()
	sizeUnits := int64(mr.cfg.Size * 1e6)

	// Price significantly below mean: BUY.
	if deviation <= -threshold {
		priceTicks := int64(mid * 1e6)
		sig := domain.TradeSignal{
			ID:         fmt.Sprintf("mr-buy-%s-%d", assetID, now.UnixNano()),
			Source:     mr.Name(),
			MarketID:   "",
			TokenID:    assetID,
			Side:       domain.OrderSideBuy,
			PriceTicks: priceTicks,
			SizeUnits:  sizeUnits,
			Urgency:    domain.SignalUrgencyMedium,
			Reason:     fmt.Sprintf("mean reversion buy: mid=%.6f avg=%.6f dev=%.2f sigma", mid, avg, deviation),
			Metadata: map[string]string{
				"avg":       fmt.Sprintf("%.6f", avg),
				"vol":       fmt.Sprintf("%.6f", vol),
				"deviation": fmt.Sprintf("%.4f", deviation),
				"threshold": fmt.Sprintf("%.4f", threshold),
			},
			CreatedAt: now,
			ExpiresAt: now.Add(60 * time.Second),
		}

		mr.logger.Info("mean reversion BUY signal",
			slog.String("asset", assetID),
			slog.Float64("mid", mid),
			slog.Float64("avg", avg),
			slog.Float64("deviation", deviation),
		)
		return []domain.TradeSignal{sig}, nil
	}

	// Price significantly above mean: SELL.
	if deviation >= threshold {
		priceTicks := int64(mid * 1e6)
		sig := domain.TradeSignal{
			ID:         fmt.Sprintf("mr-sell-%s-%d", assetID, now.UnixNano()),
			Source:     mr.Name(),
			MarketID:   "",
			TokenID:    assetID,
			Side:       domain.OrderSideSell,
			PriceTicks: priceTicks,
			SizeUnits:  sizeUnits,
			Urgency:    domain.SignalUrgencyMedium,
			Reason:     fmt.Sprintf("mean reversion sell: mid=%.6f avg=%.6f dev=%.2f sigma", mid, avg, deviation),
			Metadata: map[string]string{
				"avg":       fmt.Sprintf("%.6f", avg),
				"vol":       fmt.Sprintf("%.6f", vol),
				"deviation": fmt.Sprintf("%.4f", deviation),
				"threshold": fmt.Sprintf("%.4f", threshold),
			},
			CreatedAt: now,
			ExpiresAt: now.Add(60 * time.Second),
		}

		mr.logger.Info("mean reversion SELL signal",
			slog.String("asset", assetID),
			slog.Float64("mid", mid),
			slog.Float64("avg", avg),
			slog.Float64("deviation", deviation),
		)
		return []domain.TradeSignal{sig}, nil
	}

	return nil, nil
}

// OnPriceChange tracks the price update but does not produce signals from
// incremental level changes.
func (mr *MeanReversion) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	mr.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}

// OnTrade records the trade price in the tracker.
func (mr *MeanReversion) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	mr.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}

// OnSignal is a no-op; MeanReversion does not react to external signals.
func (mr *MeanReversion) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}

// Close releases resources. MeanReversion has nothing to release.
func (mr *MeanReversion) Close() error { return nil }

// stdDevThreshold returns the configured threshold or the default.
func (mr *MeanReversion) stdDevThreshold() float64 {
	if v, ok := mr.cfg.Params["std_dev_threshold"]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return defaultStdDevThreshold
}

// LookbackWindow returns the configured lookback duration, falling back to the
// default of 5 minutes. This can be used by callers when constructing the
// PriceTracker for this strategy.
func (mr *MeanReversion) LookbackWindow() time.Duration {
	if v, ok := mr.cfg.Params["lookback_window"]; ok {
		if s, ok := v.(string); ok {
			d, err := time.ParseDuration(s)
			if err == nil {
				return d
			}
		}
	}
	d, _ := time.ParseDuration(defaultLookbackWindow)
	return d
}
