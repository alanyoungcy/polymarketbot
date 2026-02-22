package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/google/uuid"
)

const (
	defaultMinEdgeBps   = 50
	defaultMaxGroupSize = 10
	defaultSizePerLeg   = 5.0
	defaultTTLSeconds   = 30
	defaultMaxStaleSec  = 5
)

// GroupPriceState holds YES/NO price state per market for one condition group.
type GroupPriceState struct {
	GroupID     string
	YesPrices   map[string]float64 // marketID -> YES price
	NoPrices    map[string]float64
	LastUpdate  map[string]time.Time
	LastUpdateAt time.Time
}

// RebalancingArb exploits mispricing within a single condition group (sum of YES != 1.0).
type RebalancingArb struct {
	cfg         Config
	tracker     *PriceTracker
	groups      domain.ConditionGroupStore
	markets     domain.MarketStore
	prices      domain.PriceCache
	groupStates map[string]*GroupPriceState
	mu          sync.RWMutex
	logger      *slog.Logger
}

// NewRebalancingArb creates a RebalancingArb strategy.
func NewRebalancingArb(cfg Config, tracker *PriceTracker, groups domain.ConditionGroupStore, markets domain.MarketStore, prices domain.PriceCache, logger *slog.Logger) *RebalancingArb {
	return &RebalancingArb{
		cfg:         cfg,
		tracker:     tracker,
		groups:      groups,
		markets:     markets,
		prices:      prices,
		groupStates: make(map[string]*GroupPriceState),
		logger:      logger.With(slog.String("strategy", "rebalancing_arb")),
	}
}

// Name returns the strategy identifier.
func (r *RebalancingArb) Name() string { return "rebalancing_arb" }

// Init builds tokenID -> (groupID, marketID) and preloads group state.
func (r *RebalancingArb) Init(ctx context.Context) error {
	groupList, err := r.groups.List(ctx)
	if err != nil {
		return err
	}
	maxSize := r.maxGroupSize()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, g := range groupList {
		marketIDs, err := r.groups.ListMarkets(ctx, g.ID)
		if err != nil {
			continue
		}
		if len(marketIDs) > maxSize || len(marketIDs) == 0 {
			continue
		}
		r.groupStates[g.ID] = &GroupPriceState{
			GroupID:    g.ID,
			YesPrices:  make(map[string]float64),
			NoPrices:   make(map[string]float64),
			LastUpdate: make(map[string]time.Time),
		}
	}
	return nil
}

// OnBookUpdate updates group state for the asset's group and may emit multi-leg signals.
func (r *RebalancingArb) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	yesPrice := snap.MidPrice
	if yesPrice <= 0 && snap.BestBid > 0 {
		yesPrice = snap.BestBid
	}
	// Find which group this asset (token) belongs to by scanning groups.
	groupList, err := r.groups.List(ctx)
	if err != nil {
		return nil, nil
	}
	maxSize := r.maxGroupSize()
	staleSec := time.Duration(r.maxStaleSec()) * time.Second
	now := time.Now().UTC()

	for _, g := range groupList {
		marketIDs, _ := r.groups.ListMarkets(ctx, g.ID)
		if len(marketIDs) > maxSize || len(marketIDs) == 0 {
			continue
		}
		var marketID string
		for _, mid := range marketIDs {
			mkt, err := r.markets.GetByID(ctx, mid)
			if err != nil {
				continue
			}
			if mkt.TokenIDs[0] == snap.AssetID || mkt.TokenIDs[1] == snap.AssetID {
				marketID = mid
				break
			}
		}
		if marketID == "" {
			continue
		}

		r.mu.Lock()
		state, ok := r.groupStates[g.ID]
		if !ok {
			state = &GroupPriceState{
				GroupID:    g.ID,
				YesPrices:  make(map[string]float64),
				NoPrices:   make(map[string]float64),
				LastUpdate: make(map[string]time.Time),
			}
			r.groupStates[g.ID] = state
		}
		state.YesPrices[marketID] = yesPrice
		state.NoPrices[marketID] = 1.0 - yesPrice
		state.LastUpdate[marketID] = now
		state.LastUpdateAt = now
		r.mu.Unlock()

		// Check if all markets in this group have fresh prices and sum_yes deviates
		signals, err := r.checkGroup(ctx, g.ID, marketIDs, state, staleSec, now)
		if err != nil {
			return nil, err
		}
		if len(signals) > 0 {
			return signals, nil
		}
	}
	return nil, nil
}

func (r *RebalancingArb) checkGroup(ctx context.Context, groupID string, marketIDs []string, state *GroupPriceState, maxStale time.Duration, now time.Time) ([]domain.TradeSignal, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sumYes float64
	allFresh := true
	for _, mid := range marketIDs {
		t, ok := state.LastUpdate[mid]
		if !ok || now.Sub(t) > maxStale {
			allFresh = false
			break
		}
		sumYes += state.YesPrices[mid]
	}
	if !allFresh {
		return nil, nil
	}
	minEdge := float64(r.minEdgeBps()) / 10_000
	if sumYes >= 1.0-minEdge && sumYes <= 1.0+minEdge {
		return nil, nil
	}

	sizePerLeg := r.sizePerLeg()
	ttl := time.Duration(r.ttlSeconds()) * time.Second
	legGroupID := uuid.New().String()
	policy := string(domain.LegPolicyAllOrNone)

	var signals []domain.TradeSignal
	if sumYes < 1.0-minEdge {
		// Long the group: BUY YES on all outcomes
		for _, mid := range marketIDs {
			mkt, err := r.markets.GetByID(ctx, mid)
			if err != nil {
				continue
			}
			yesTokenID := mkt.TokenIDs[0]
			price := state.YesPrices[mid]
			signals = append(signals, domain.TradeSignal{
				ID:         fmt.Sprintf("ra-buy-%s-%d", mid, now.UnixNano()),
				Source:     r.Name(),
				MarketID:   mid,
				TokenID:    yesTokenID,
				Side:       domain.OrderSideBuy,
				PriceTicks: int64(price * 1e6),
				SizeUnits:  int64(sizePerLeg * 1e6),
				Urgency:    domain.SignalUrgencyHigh,
				Reason:     fmt.Sprintf("rebalancing_arb sum_yes=%.4f < 1-min_edge", sumYes),
				Metadata: map[string]string{
					"leg_group_id": legGroupID,
					"leg_count":    fmt.Sprintf("%d", len(marketIDs)),
					"leg_policy":  policy,
				},
				CreatedAt: now,
				ExpiresAt:  now.Add(ttl),
			})
		}
	} else if sumYes > 1.0+minEdge {
		// Short the group: SELL YES on all outcomes
		for _, mid := range marketIDs {
			mkt, err := r.markets.GetByID(ctx, mid)
			if err != nil {
				continue
			}
			yesTokenID := mkt.TokenIDs[0]
			price := state.YesPrices[mid]
			signals = append(signals, domain.TradeSignal{
				ID:         fmt.Sprintf("ra-sell-%s-%d", mid, now.UnixNano()),
				Source:     r.Name(),
				MarketID:   mid,
				TokenID:    yesTokenID,
				Side:       domain.OrderSideSell,
				PriceTicks: int64(price * 1e6),
				SizeUnits:  int64(sizePerLeg * 1e6),
				Urgency:    domain.SignalUrgencyHigh,
				Reason:     fmt.Sprintf("rebalancing_arb sum_yes=%.4f > 1+min_edge", sumYes),
				Metadata: map[string]string{
					"leg_group_id": legGroupID,
					"leg_count":    fmt.Sprintf("%d", len(marketIDs)),
					"leg_policy":  policy,
				},
				CreatedAt: now,
				ExpiresAt:  now.Add(ttl),
			})
		}
	}
	return signals, nil
}

func (r *RebalancingArb) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	r.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}
func (r *RebalancingArb) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	r.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}
func (r *RebalancingArb) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}
func (r *RebalancingArb) Close() error { return nil }

func (r *RebalancingArb) minEdgeBps() int {
	if v, ok := r.cfg.Params["min_edge_bps"].(int); ok {
		return v
	}
	if v, ok := r.cfg.Params["min_edge_bps"].(int64); ok {
		return int(v)
	}
	return defaultMinEdgeBps
}
func (r *RebalancingArb) maxGroupSize() int {
	if v, ok := r.cfg.Params["max_group_size"].(int); ok {
		return v
	}
	return defaultMaxGroupSize
}
func (r *RebalancingArb) sizePerLeg() float64 {
	if v, ok := r.cfg.Params["size_per_leg"].(float64); ok {
		return v
	}
	return defaultSizePerLeg
}
func (r *RebalancingArb) ttlSeconds() int {
	if v, ok := r.cfg.Params["ttl_seconds"].(int); ok {
		return v
	}
	return defaultTTLSeconds
}
func (r *RebalancingArb) maxStaleSec() int {
	if v, ok := r.cfg.Params["max_stale_sec"].(int); ok {
		return v
	}
	return defaultMaxStaleSec
}
