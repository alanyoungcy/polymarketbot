package arbitrage

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// SpreadConfig configures the spread arbitrage strategy.
type SpreadConfig struct {
	MinSpreadBps   float64 // minimum bid-ask spread in bps to consider
	MinSize        float64 // minimum size at best bid or ask
	EstFeeBps      float64
	EstSlippageBps float64
	EstLatencyBps  float64
	MaxAmount      float64
}

// Spread detects opportunities when the bid-ask spread is wide enough to
// capture edge after fees (single-venue: buy at bid, sell at ask is not
// applicable; we treat "spread" as opportunity to provide liquidity or
// capture mean reversion when spread exceeds threshold).
type Spread struct {
	cfg    SpreadConfig
	logger *slog.Logger
}

// NewSpread creates a spread arbitrage strategy.
func NewSpread(cfg SpreadConfig, logger *slog.Logger) *Spread {
	return &Spread{cfg: cfg, logger: logger.With(slog.String("arb_strategy", "spread"))}
}

// Name returns the strategy identifier.
func (s *Spread) Name() string { return "spread" }

// Detect returns opportunities when best_ask - best_bid in bps >= MinSpreadBps
// and size at BBO meets minimum.
func (s *Spread) Detect(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.ArbOpportunity, error) {
	if snap.BestBid <= 0 || snap.BestAsk <= 0 {
		return nil, nil
	}
	spread := snap.BestAsk - snap.BestBid
	if spread <= 0 {
		return nil, nil
	}
	mid := (snap.BestBid + snap.BestAsk) / 2
	if mid <= 0 {
		return nil, nil
	}
	spreadBps := (spread / mid) * 10000
	if spreadBps < s.cfg.MinSpreadBps {
		return nil, nil
	}
	// Use best bid size and best ask size from first level if available.
	var bidSize, askSize float64
	if len(snap.Bids) > 0 {
		bidSize = snap.Bids[0].Size
	}
	if len(snap.Asks) > 0 {
		askSize = snap.Asks[0].Size
	}
	minSize := bidSize
	if askSize < minSize {
		minSize = askSize
	}
	if minSize < s.cfg.MinSize {
		return nil, nil
	}
	maxAmount := s.cfg.MaxAmount
	if minSize*mid < maxAmount {
		maxAmount = minSize * mid
	}
	grossEdgeBps := spreadBps
	netEdgeBps := grossEdgeBps - s.cfg.EstFeeBps - s.cfg.EstSlippageBps - s.cfg.EstLatencyBps
	if netEdgeBps <= 0 {
		return nil, nil
	}
	notional := maxAmount
	expectedPnL := notional * (netEdgeBps / 10000)

	opp := domain.ArbOpportunity{
		ID:             uuid.Must(uuid.NewRandom()).String(),
		PolyMarketID:   snap.AssetID,
		PolyTokenID:    snap.AssetID,
		PolyPrice:      mid,
		KalshiMarketID: "",
		KalshiPrice:    0,
		GrossEdgeBps:   grossEdgeBps,
		Direction:      "spread",
		MaxAmount:      maxAmount,
		EstFeeBps:      s.cfg.EstFeeBps,
		EstSlippageBps: s.cfg.EstSlippageBps,
		EstLatencyBps:  s.cfg.EstLatencyBps,
		NetEdgeBps:     netEdgeBps,
		ExpectedPnLUSD: expectedPnL,
		DetectedAt:     snap.Timestamp,
		Duration:       0,
		Executed:       false,
	}
	s.logger.DebugContext(ctx, "spread opportunity detected",
		slog.String("asset_id", snap.AssetID),
		slog.Float64("spread_bps", spreadBps),
		slog.Float64("net_edge_bps", netEdgeBps),
	)
	return []domain.ArbOpportunity{opp}, nil
}
