package arbitrage

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ImbalanceConfig configures the orderbook imbalance strategy.
type ImbalanceConfig struct {
	RatioThreshold   float64 // e.g. 1.5 = bid_vol/ask_vol or ask_vol/bid_vol must exceed this
	MinTotalVolume  float64 // minimum total volume (bid+ask) to consider
	EstFeeBps        float64
	EstSlippageBps   float64
	EstLatencyBps    float64
	MaxAmount        float64
	EdgeBpsPerRatio  float64 // gross edge in bps per unit ratio above 1.0 (e.g. 10 bps per 0.5 ratio)
}

// Imbalance detects opportunities when orderbook volume is skewed (e.g. much
// more bid volume than ask volume suggests buying pressure / mean reversion).
type Imbalance struct {
	cfg    ImbalanceConfig
	logger *slog.Logger
}

// NewImbalance creates an imbalance arbitrage strategy.
func NewImbalance(cfg ImbalanceConfig, logger *slog.Logger) *Imbalance {
	return &Imbalance{cfg: cfg, logger: logger.With(slog.String("arb_strategy", "imbalance"))}
}

// Name returns the strategy identifier.
func (i *Imbalance) Name() string { return "imbalance" }

// Detect returns opportunities when bid/ask volume ratio exceeds threshold.
func (i *Imbalance) Detect(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.ArbOpportunity, error) {
	var bidVol, askVol float64
	for _, l := range snap.Bids {
		bidVol += l.Price * l.Size
	}
	for _, l := range snap.Asks {
		askVol += l.Price * l.Size
	}
	total := bidVol + askVol
	if total < i.cfg.MinTotalVolume {
		return nil, nil
	}
	if bidVol <= 0 || askVol <= 0 {
		return nil, nil
	}
	ratio := bidVol / askVol
	var direction string
	var grossEdgeBps float64
	if ratio >= i.cfg.RatioThreshold {
		direction = "imbalance_buy"
		grossEdgeBps = (ratio - 1.0) * i.cfg.EdgeBpsPerRatio
	} else if 1.0/ratio >= i.cfg.RatioThreshold {
		direction = "imbalance_sell"
		grossEdgeBps = (1.0/ratio - 1.0) * i.cfg.EdgeBpsPerRatio
	} else {
		return nil, nil
	}
	if grossEdgeBps <= 0 {
		return nil, nil
	}
	netEdgeBps := grossEdgeBps - i.cfg.EstFeeBps - i.cfg.EstSlippageBps - i.cfg.EstLatencyBps
	if netEdgeBps <= 0 {
		return nil, nil
	}
	mid := snap.MidPrice
	if mid <= 0 {
		mid = (snap.BestBid + snap.BestAsk) / 2
	}
	if mid <= 0 {
		return nil, nil
	}
	maxAmount := i.cfg.MaxAmount
	expectedPnL := maxAmount * (netEdgeBps / 10000)

	opp := domain.ArbOpportunity{
		ID:             uuid.Must(uuid.NewRandom()).String(),
		PolyMarketID:   snap.AssetID,
		PolyTokenID:    snap.AssetID,
		PolyPrice:      mid,
		KalshiMarketID:  "",
		KalshiPrice:     0,
		GrossEdgeBps:   grossEdgeBps,
		Direction:      direction,
		MaxAmount:      maxAmount,
		EstFeeBps:      i.cfg.EstFeeBps,
		EstSlippageBps: i.cfg.EstSlippageBps,
		EstLatencyBps:  i.cfg.EstLatencyBps,
		NetEdgeBps:     netEdgeBps,
		ExpectedPnLUSD: expectedPnL,
		DetectedAt:     snap.Timestamp,
		Duration:       0,
		Executed:       false,
	}
	i.logger.DebugContext(ctx, "imbalance opportunity detected",
		slog.String("asset_id", snap.AssetID),
		slog.String("direction", direction),
		slog.Float64("ratio", ratio),
		slog.Float64("net_edge_bps", netEdgeBps),
	)
	return []domain.ArbOpportunity{opp}, nil
}
