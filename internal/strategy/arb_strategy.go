package strategy

import (
	"context"
	"log/slog"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ArbStrategy passes through arbitrage signals that originate from an external
// detector (identified by Source == "arb_detector"). It does not generate
// signals from orderbook or trade data itself.
type ArbStrategy struct {
	cfg    Config
	logger *slog.Logger
}

// NewArbStrategy creates an ArbStrategy with the given configuration.
func NewArbStrategy(cfg Config, logger *slog.Logger) *ArbStrategy {
	return &ArbStrategy{
		cfg:    cfg,
		logger: logger.With(slog.String("strategy", "arb")),
	}
}

// Name returns the strategy identifier.
func (a *ArbStrategy) Name() string { return "arb" }

// Init performs one-time setup. ArbStrategy requires no initialisation.
func (a *ArbStrategy) Init(_ context.Context) error { return nil }

// OnBookUpdate is a no-op; arbitrage signals come from the external arb
// detector, not from a single orderbook.
func (a *ArbStrategy) OnBookUpdate(_ context.Context, _ domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	return nil, nil
}

// OnPriceChange is a no-op for ArbStrategy.
func (a *ArbStrategy) OnPriceChange(_ context.Context, _ domain.PriceChange) ([]domain.TradeSignal, error) {
	return nil, nil
}

// OnTrade is a no-op for ArbStrategy.
func (a *ArbStrategy) OnTrade(_ context.Context, _ domain.Trade) ([]domain.TradeSignal, error) {
	return nil, nil
}

// OnSignal inspects an incoming signal. If the signal's source is
// "arb_detector" the strategy promotes it to Immediate urgency and passes it
// through. All other signals are ignored.
func (a *ArbStrategy) OnSignal(_ context.Context, signal domain.TradeSignal) ([]domain.TradeSignal, error) {
	if signal.Source != "arb_detector" {
		return nil, nil
	}

	// Promote urgency so the executor processes it without delay.
	signal.Urgency = domain.SignalUrgencyImmediate

	a.logger.Info("arb signal forwarded",
		slog.String("signal_id", signal.ID),
		slog.String("token", signal.TokenID),
		slog.String("side", string(signal.Side)),
		slog.Float64("price", signal.Price()),
	)

	return []domain.TradeSignal{signal}, nil
}

// Close releases resources. ArbStrategy has nothing to release.
func (a *ArbStrategy) Close() error { return nil }
