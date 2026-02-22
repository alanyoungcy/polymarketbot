package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/google/uuid"
)

const (
	defaultComboMinEdgeBps = 100
	defaultMaxRelations    = 10
	defaultComboSizePerLeg = 5.0
)

// RelationComputer computes implied target prices from source group prices (used by combinatorial_arb).
type RelationComputer interface {
	ComputeImpliedPrices(ctx context.Context, sourceGroupID string, sourcePrices map[string]float64, targetGroupID string) (map[string]float64, error)
}

// CombinatorialArb exploits mispricing between related condition groups.
type CombinatorialArb struct {
	cfg        Config
	tracker    *PriceTracker
	groups     domain.ConditionGroupStore
	relations  domain.MarketRelationStore
	relSvc     RelationComputer
	markets    domain.MarketStore
	prices     domain.PriceCache
	mu         sync.Mutex
	logger     *slog.Logger
}

// NewCombinatorialArb creates a CombinatorialArb strategy.
func NewCombinatorialArb(cfg Config, tracker *PriceTracker, groups domain.ConditionGroupStore, relations domain.MarketRelationStore, relSvc RelationComputer, markets domain.MarketStore, prices domain.PriceCache, logger *slog.Logger) *CombinatorialArb {
	return &CombinatorialArb{
		cfg:       cfg,
		tracker:   tracker,
		groups:    groups,
		relations: relations,
		relSvc:    relSvc,
		markets:   markets,
		prices:    prices,
		logger:    logger.With(slog.String("strategy", "combinatorial_arb")),
	}
}

// Name returns the strategy identifier.
func (c *CombinatorialArb) Name() string { return "combinatorial_arb" }

// Init is a no-op.
func (c *CombinatorialArb) Init(_ context.Context) error { return nil }

// OnBookUpdate checks relations involving this asset and may emit multi-leg signals.
func (c *CombinatorialArb) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	if c.relSvc == nil {
		return nil, nil
	}
	relList, err := c.relations.List(ctx)
	if err != nil {
		return nil, nil
	}
	maxRels := c.maxRelations()
	minEdgeBps := float64(c.minEdgeBps()) // in bps
	sizePerLeg := c.sizePerLeg()
	now := time.Now().UTC()
	ttl := 30 * time.Second
	legGroupID := uuid.New().String()
	policy := string(domain.LegPolicyAllOrNone)

	var allSignals []domain.TradeSignal
	seen := 0
	for _, rel := range relList {
		if seen >= maxRels {
			break
		}
		sourceMarketIDs, _ := c.groups.ListMarkets(ctx, rel.SourceGroupID)
		targetMarketIDs, _ := c.groups.ListMarkets(ctx, rel.TargetGroupID)
		if len(sourceMarketIDs) == 0 || len(targetMarketIDs) == 0 {
			continue
		}
		// Build source prices (market ID -> YES price) from PriceCache. We need token ID per source market.
		sourcePrices := make(map[string]float64)
		for _, mid := range sourceMarketIDs {
			mkt, err := c.markets.GetByID(ctx, mid)
			if err != nil {
				continue
			}
			tokenID := mkt.TokenIDs[0]
			p, _, err := c.prices.GetPrice(ctx, tokenID)
			if err != nil || p < 0 {
				continue
			}
			sourcePrices[mid] = p
		}
		if len(sourcePrices) == 0 {
			continue
		}
		implied, err := c.relSvc.ComputeImpliedPrices(ctx, rel.SourceGroupID, sourcePrices, rel.TargetGroupID)
		if err != nil {
			continue
		}
		seen++
		for _, targetMid := range targetMarketIDs {
			impliedPrice, ok := implied[targetMid]
			if !ok || impliedPrice <= 0 {
				continue
			}
			mkt, err := c.markets.GetByID(ctx, targetMid)
			if err != nil {
				continue
			}
			yesTokenID := mkt.TokenIDs[0]
			actualPrice, _, err := c.prices.GetPrice(ctx, yesTokenID)
			if err != nil {
				continue
			}
			deviationBps := math.Abs(actualPrice-impliedPrice) / impliedPrice * 10_000
			if deviationBps < minEdgeBps {
				continue
			}
			var side domain.OrderSide
			if actualPrice < impliedPrice {
				side = domain.OrderSideBuy
			} else {
				side = domain.OrderSideSell
			}
			allSignals = append(allSignals, domain.TradeSignal{
				ID:         fmt.Sprintf("ca-%s-%d", targetMid, now.UnixNano()),
				Source:     c.Name(),
				MarketID:   targetMid,
				TokenID:    yesTokenID,
				Side:       side,
				PriceTicks: int64(actualPrice * 1e6),
				SizeUnits:  int64(sizePerLeg * 1e6),
				Urgency:    domain.SignalUrgencyHigh,
				Reason:     fmt.Sprintf("combinatorial_arb deviation_bps=%.0f", deviationBps),
				Metadata: map[string]string{
					"leg_group_id": legGroupID,
					"leg_policy":   policy,
				},
				CreatedAt: now,
				ExpiresAt: now.Add(ttl),
			})
		}
	}
	return allSignals, nil
}

func (c *CombinatorialArb) minEdgeBps() int {
	if v, ok := c.cfg.Params["min_edge_bps"].(int); ok {
		return v
	}
	if v, ok := c.cfg.Params["min_edge_bps"].(int64); ok {
		return int(v)
	}
	return defaultComboMinEdgeBps
}
func (c *CombinatorialArb) maxRelations() int {
	if v, ok := c.cfg.Params["max_relations"].(int); ok {
		return v
	}
	if v, ok := c.cfg.Params["max_relations"].(int64); ok {
		return int(v)
	}
	return defaultMaxRelations
}
func (c *CombinatorialArb) sizePerLeg() float64 {
	if v, ok := c.cfg.Params["size_per_leg"].(float64); ok {
		return v
	}
	return defaultComboSizePerLeg
}

func (c *CombinatorialArb) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	c.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}
func (c *CombinatorialArb) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	c.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}
func (c *CombinatorialArb) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}
func (c *CombinatorialArb) Close() error { return nil }
