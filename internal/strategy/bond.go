package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

const (
	defaultMinYesPrice     = 0.95
	defaultMinAPR          = 0.10
	defaultMinVolume       = 100_000
	defaultMaxDaysToExp    = 90
	defaultMinDaysToExp    = 7
	defaultMaxPositions    = 10
	defaultSizePerPosition = 50.0
)

// BondStrategy buys high-probability YES tokens and holds to resolution (bond-like).
type BondStrategy struct {
	cfg     Config
	tracker *PriceTracker
	bonds   domain.BondPositionStore
	markets domain.MarketStore
	logger  *slog.Logger
}

// NewBondStrategy creates a BondStrategy.
func NewBondStrategy(cfg Config, tracker *PriceTracker, bonds domain.BondPositionStore, markets domain.MarketStore, logger *slog.Logger) *BondStrategy {
	return &BondStrategy{
		cfg:     cfg,
		tracker: tracker,
		bonds:   bonds,
		markets: markets,
		logger:  logger.With(slog.String("strategy", "bond")),
	}
}

// Name returns the strategy identifier.
func (b *BondStrategy) Name() string { return "bond" }

// Init is a no-op.
func (b *BondStrategy) Init(_ context.Context) error { return nil }

// OnBookUpdate checks if the asset qualifies as a bond (high YES price, APR, volume, expiry) and emits BUY.
func (b *BondStrategy) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	yesPrice := snap.MidPrice
	if yesPrice <= 0 && snap.BestBid > 0 {
		yesPrice = snap.BestBid
	}
	if yesPrice < b.minYesPrice() {
		return nil, nil
	}
	// Resolve asset to market (we have token ID; need market for volume/end date)
	mkt, err := b.markets.GetByTokenID(ctx, snap.AssetID)
	if err != nil {
		return nil, nil
	}
	vol := mkt.Volume
	if vol < b.minVolume() {
		return nil, nil
	}
	var daysToExp float64
	if mkt.ClosedAt != nil {
		daysToExp = mkt.ClosedAt.Sub(time.Now().UTC()).Hours() / 24
	} else {
		return nil, nil
	}
	minDays := float64(b.minDaysToExp())
	maxDays := float64(b.maxDaysToExp())
	if daysToExp < minDays || daysToExp > maxDays {
		return nil, nil
	}
	yield := (1.0 - yesPrice) / yesPrice
	if yield <= 0 {
		return nil, nil
	}
	apr := yield * (365 / daysToExp)
	if apr < b.minAPR() {
		return nil, nil
	}
	open, err := b.bonds.GetOpen(ctx)
	if err != nil {
		return nil, err
	}
	if len(open) >= b.maxPositions() {
		return nil, nil
	}
	size := b.sizePerPosition()
	now := time.Now().UTC()
	sig := domain.TradeSignal{
		ID:         fmt.Sprintf("bond-%s-%d", mkt.ID, now.UnixNano()),
		Source:     b.Name(),
		MarketID:   mkt.ID,
		TokenID:    snap.AssetID,
		Side:       domain.OrderSideBuy,
		PriceTicks: int64(yesPrice * 1e6),
		SizeUnits:  int64(size * 1e6),
		Urgency:    domain.SignalUrgencyMedium,
		Reason:     fmt.Sprintf("bond apr=%.2f%% yes=%.4f days=%.0f", apr*100, yesPrice, daysToExp),
		Metadata:   map[string]string{"expected_apr": fmt.Sprintf("%.4f", apr)},
		CreatedAt:  now,
		ExpiresAt:  now.Add(5 * time.Minute),
	}
	return []domain.TradeSignal{sig}, nil
}

func (b *BondStrategy) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	b.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}
func (b *BondStrategy) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	b.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}
func (b *BondStrategy) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}
func (b *BondStrategy) Close() error { return nil }

func (b *BondStrategy) minYesPrice() float64 {
	if v, ok := b.cfg.Params["min_yes_price"].(float64); ok {
		return v
	}
	return defaultMinYesPrice
}
func (b *BondStrategy) minAPR() float64 {
	if v, ok := b.cfg.Params["min_apr"].(float64); ok {
		return v
	}
	return defaultMinAPR
}
func (b *BondStrategy) minVolume() float64 {
	if v, ok := b.cfg.Params["min_volume"].(float64); ok {
		return v
	}
	return defaultMinVolume
}
func (b *BondStrategy) maxDaysToExp() int {
	if v, ok := b.cfg.Params["max_days_to_exp"].(int); ok {
		return v
	}
	if v, ok := b.cfg.Params["max_days_to_exp"].(int64); ok {
		return int(v)
	}
	return defaultMaxDaysToExp
}
func (b *BondStrategy) minDaysToExp() int {
	if v, ok := b.cfg.Params["min_days_to_exp"].(int); ok {
		return v
	}
	if v, ok := b.cfg.Params["min_days_to_exp"].(int64); ok {
		return int(v)
	}
	return defaultMinDaysToExp
}
func (b *BondStrategy) maxPositions() int {
	if v, ok := b.cfg.Params["max_positions"].(int); ok {
		return v
	}
	if v, ok := b.cfg.Params["max_positions"].(int64); ok {
		return int(v)
	}
	return defaultMaxPositions
}
func (b *BondStrategy) sizePerPosition() float64 {
	if v, ok := b.cfg.Params["size_per_position"].(float64); ok {
		return v
	}
	return defaultSizePerPosition
}