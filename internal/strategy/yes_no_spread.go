package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

const (
	defaultYesNoMinEdgeBps = 40
	defaultYesNoSizePerLeg = 5.0
	defaultYesNoTTLSeconds = 30
	defaultYesNoMaxStale   = 5
	defaultYesNoCooldown   = 2
)

// YesNoSpread detects classic binary Dutch-book opportunities:
// buy YES+NO when ask_yes+ask_no < 1-edge, or sell both when bid_yes+bid_no > 1+edge.
type YesNoSpread struct {
	cfg     Config
	tracker *PriceTracker
	markets domain.MarketStore
	books   domain.OrderbookCache
	logger  *slog.Logger

	mu       sync.Mutex
	lastEmit map[string]time.Time // marketID -> last signal time
}

// NewYesNoSpread creates a yes/no spread strategy.
func NewYesNoSpread(cfg Config, tracker *PriceTracker, markets domain.MarketStore, books domain.OrderbookCache, logger *slog.Logger) *YesNoSpread {
	return &YesNoSpread{
		cfg:      cfg,
		tracker:  tracker,
		markets:  markets,
		books:    books,
		logger:   logger.With(slog.String("strategy", "yes_no_spread")),
		lastEmit: make(map[string]time.Time),
	}
}

// Name returns the strategy identifier.
func (y *YesNoSpread) Name() string { return "yes_no_spread" }

// Init is a no-op.
func (y *YesNoSpread) Init(_ context.Context) error { return nil }

// OnBookUpdate evaluates paired YES/NO token prices for the same market.
func (y *YesNoSpread) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	if y.markets == nil || y.books == nil {
		return nil, nil
	}

	mkt, err := y.markets.GetByTokenID(ctx, snap.AssetID)
	if err != nil {
		return nil, nil
	}
	yesToken, noToken := mkt.TokenIDs[0], mkt.TokenIDs[1]
	if yesToken == "" || noToken == "" {
		return nil, nil
	}

	now := time.Now().UTC()
	maxStale := time.Duration(y.maxStaleSec()) * time.Second
	yesSnap, err := y.snapshotForToken(ctx, snap, yesToken)
	if err != nil || yesSnap.AssetID == "" || now.Sub(yesSnap.Timestamp) > maxStale {
		return nil, nil
	}
	noSnap, err := y.snapshotForToken(ctx, snap, noToken)
	if err != nil || noSnap.AssetID == "" || now.Sub(noSnap.Timestamp) > maxStale {
		return nil, nil
	}

	yesAsk, yesBid := bestAsk(yesSnap), bestBid(yesSnap)
	noAsk, noBid := bestAsk(noSnap), bestBid(noSnap)
	minEdge := float64(y.minEdgeBps()) / 10_000

	emit := func(side domain.OrderSide, yesPx, noPx, edge float64, reasonFmt string) []domain.TradeSignal {
		sizePerLeg := y.sizePerLeg()
		ttl := time.Duration(y.ttlSeconds()) * time.Second
		legGroupID := uuid.New().String()
		signals := []domain.TradeSignal{
			{
				ID:         fmt.Sprintf("yn-%s-yes-%d", side, now.UnixNano()),
				Source:     y.Name(),
				MarketID:   mkt.ID,
				TokenID:    yesToken,
				Side:       side,
				PriceTicks: int64(yesPx * 1e6),
				SizeUnits:  int64(sizePerLeg * 1e6),
				Urgency:    domain.SignalUrgencyImmediate,
				Reason:     fmt.Sprintf(reasonFmt, yesPx+noPx, edge*10_000),
				Metadata: map[string]string{
					"leg_group_id": legGroupID,
					"leg_count":    "2",
					"leg_policy":   string(domain.LegPolicyAllOrNone),
					"arb_type":     string(domain.ArbTypeRebalancing),
				},
				CreatedAt: now,
				ExpiresAt: now.Add(ttl),
			},
			{
				ID:         fmt.Sprintf("yn-%s-no-%d", side, now.UnixNano()),
				Source:     y.Name(),
				MarketID:   mkt.ID,
				TokenID:    noToken,
				Side:       side,
				PriceTicks: int64(noPx * 1e6),
				SizeUnits:  int64(sizePerLeg * 1e6),
				Urgency:    domain.SignalUrgencyImmediate,
				Reason:     fmt.Sprintf(reasonFmt, yesPx+noPx, edge*10_000),
				Metadata: map[string]string{
					"leg_group_id": legGroupID,
					"leg_count":    "2",
					"leg_policy":   string(domain.LegPolicyAllOrNone),
					"arb_type":     string(domain.ArbTypeRebalancing),
				},
				CreatedAt: now,
				ExpiresAt: now.Add(ttl),
			},
		}
		return signals
	}

	if y.recentlyEmitted(mkt.ID, now) {
		return nil, nil
	}

	if yesAsk > 0 && noAsk > 0 {
		sumAsk := yesAsk + noAsk
		edge := 1.0 - sumAsk
		if edge > minEdge {
			y.markEmitted(mkt.ID, now)
			return emit(
				domain.OrderSideBuy,
				yesAsk,
				noAsk,
				edge,
				"yes_no_spread buy_pair sum_ask=%.4f edge_bps=%.1f",
			), nil
		}
	}

	if yesBid > 0 && noBid > 0 {
		sumBid := yesBid + noBid
		edge := sumBid - 1.0
		if edge > minEdge {
			y.markEmitted(mkt.ID, now)
			return emit(
				domain.OrderSideSell,
				yesBid,
				noBid,
				edge,
				"yes_no_spread sell_pair sum_bid=%.4f edge_bps=%.1f",
			), nil
		}
	}

	return nil, nil
}

func (y *YesNoSpread) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	y.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}

func (y *YesNoSpread) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	y.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}

func (y *YesNoSpread) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}

func (y *YesNoSpread) Close() error { return nil }

func (y *YesNoSpread) snapshotForToken(ctx context.Context, current domain.OrderbookSnapshot, tokenID string) (domain.OrderbookSnapshot, error) {
	if current.AssetID == tokenID {
		return current, nil
	}
	return y.books.GetSnapshot(ctx, tokenID)
}

func (y *YesNoSpread) recentlyEmitted(marketID string, now time.Time) bool {
	y.mu.Lock()
	defer y.mu.Unlock()
	last, ok := y.lastEmit[marketID]
	if !ok {
		return false
	}
	return now.Sub(last) < time.Duration(y.cooldownSec())*time.Second
}

func (y *YesNoSpread) markEmitted(marketID string, now time.Time) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.lastEmit[marketID] = now
}

func (y *YesNoSpread) minEdgeBps() int {
	if v, ok := y.cfg.Params["min_edge_bps"].(int); ok {
		return v
	}
	if v, ok := y.cfg.Params["min_edge_bps"].(int64); ok {
		return int(v)
	}
	if v, ok := y.cfg.Params["min_edge_bps"].(float64); ok {
		return int(v)
	}
	return defaultYesNoMinEdgeBps
}

func (y *YesNoSpread) sizePerLeg() float64 {
	if v, ok := y.cfg.Params["size_per_leg"].(float64); ok {
		return v
	}
	if v, ok := y.cfg.Params["size_per_leg"].(int); ok {
		return float64(v)
	}
	if v, ok := y.cfg.Params["size_per_leg"].(int64); ok {
		return float64(v)
	}
	return defaultYesNoSizePerLeg
}

func (y *YesNoSpread) ttlSeconds() int {
	if v, ok := y.cfg.Params["ttl_seconds"].(int); ok {
		return v
	}
	if v, ok := y.cfg.Params["ttl_seconds"].(int64); ok {
		return int(v)
	}
	if v, ok := y.cfg.Params["ttl_seconds"].(float64); ok {
		return int(v)
	}
	return defaultYesNoTTLSeconds
}

func (y *YesNoSpread) maxStaleSec() int {
	if v, ok := y.cfg.Params["max_stale_sec"].(int); ok {
		return v
	}
	if v, ok := y.cfg.Params["max_stale_sec"].(int64); ok {
		return int(v)
	}
	if v, ok := y.cfg.Params["max_stale_sec"].(float64); ok {
		return int(v)
	}
	return defaultYesNoMaxStale
}

func (y *YesNoSpread) cooldownSec() int {
	if v, ok := y.cfg.Params["cooldown_sec"].(int); ok {
		return v
	}
	if v, ok := y.cfg.Params["cooldown_sec"].(int64); ok {
		return int(v)
	}
	if v, ok := y.cfg.Params["cooldown_sec"].(float64); ok {
		return int(v)
	}
	return defaultYesNoCooldown
}

func bestAsk(s domain.OrderbookSnapshot) float64 {
	if s.BestAsk > 0 {
		return s.BestAsk
	}
	if len(s.Asks) > 0 && s.Asks[0].Price > 0 {
		return s.Asks[0].Price
	}
	return 0
}

func bestBid(s domain.OrderbookSnapshot) float64 {
	if s.BestBid > 0 {
		return s.BestBid
	}
	if len(s.Bids) > 0 && s.Bids[0].Price > 0 {
		return s.Bids[0].Price
	}
	return 0
}
