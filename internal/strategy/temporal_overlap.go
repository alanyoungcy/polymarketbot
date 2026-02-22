package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

const (
	defaultTemporalMinEdgeBps  = 80
	defaultTemporalSizePerLeg  = 5.0
	defaultTemporalTTLSeconds  = 30
	defaultTemporalMaxStaleSec = 6
	defaultTemporalCooldownSec = 3
	defaultTemporalRefreshMins = 10
	defaultTemporalMaxPairs    = 100
)

var temporalMinutesRE = regexp.MustCompile(`(?i)(\d{1,3})\s*(m|min|mins|minute|minutes)\b`)

type temporalDescriptor struct {
	marketID  string
	tokenID   string
	asset     string
	direction string // "up" or "down"
	minutes   int
}

type temporalPair struct {
	id            string
	longMarketID  string
	longTokenID   string
	longMinutes   int
	shortMarketID string
	shortTokenID  string
	shortMinutes  int
	asset         string
}

// TemporalOverlap detects opportunities between short and long horizon markets
// (e.g. long-window UP + short-window DOWN).
type TemporalOverlap struct {
	cfg     Config
	tracker *PriceTracker
	markets domain.MarketStore
	books   domain.OrderbookCache
	logger  *slog.Logger

	mu          sync.Mutex
	pairs       []temporalPair
	byToken     map[string][]temporalPair
	lastRefresh time.Time
	lastEmit    map[string]time.Time // pair ID -> timestamp
}

// NewTemporalOverlap creates a temporal-overlap strategy.
func NewTemporalOverlap(cfg Config, tracker *PriceTracker, markets domain.MarketStore, books domain.OrderbookCache, logger *slog.Logger) *TemporalOverlap {
	return &TemporalOverlap{
		cfg:      cfg,
		tracker:  tracker,
		markets:  markets,
		books:    books,
		logger:   logger.With(slog.String("strategy", "temporal_overlap")),
		byToken:  make(map[string][]temporalPair),
		lastEmit: make(map[string]time.Time),
	}
}

// Name returns the strategy identifier.
func (t *TemporalOverlap) Name() string { return "temporal_overlap" }

// Init discovers initial overlap pairs.
func (t *TemporalOverlap) Init(ctx context.Context) error {
	return t.refreshPairs(ctx, time.Now().UTC(), true)
}

// OnBookUpdate checks overlap pairs that include this token and emits
// multi-leg buy/sell bundles on detected spread violations.
func (t *TemporalOverlap) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	if t.markets == nil || t.books == nil {
		return nil, nil
	}
	now := time.Now().UTC()
	if now.Sub(t.lastRefresh) > time.Duration(t.refreshMinutes())*time.Minute {
		_ = t.refreshPairs(ctx, now, false)
	}

	t.mu.Lock()
	candidates := append([]temporalPair(nil), t.byToken[snap.AssetID]...)
	t.mu.Unlock()
	if len(candidates) == 0 {
		return nil, nil
	}

	minEdge := float64(t.minEdgeBps()) / 10_000
	maxStale := time.Duration(t.maxStaleSec()) * time.Second
	ttl := time.Duration(t.ttlSeconds()) * time.Second
	size := t.sizePerLeg()

	for _, p := range candidates {
		if t.recentlyEmitted(p.id, now) {
			continue
		}

		longSnap, err := t.snapshotForToken(ctx, snap, p.longTokenID)
		if err != nil || longSnap.AssetID == "" || now.Sub(longSnap.Timestamp) > maxStale {
			continue
		}
		shortSnap, err := t.snapshotForToken(ctx, snap, p.shortTokenID)
		if err != nil || shortSnap.AssetID == "" || now.Sub(shortSnap.Timestamp) > maxStale {
			continue
		}

		longAsk, longBid := bestAsk(longSnap), bestBid(longSnap)
		shortAsk, shortBid := bestAsk(shortSnap), bestBid(shortSnap)

		if longAsk > 0 && shortAsk > 0 {
			sumAsk := longAsk + shortAsk
			edge := 1.0 - sumAsk
			if edge > minEdge {
				t.markEmitted(p.id, now)
				return temporalPairSignals(p, domain.OrderSideBuy, longAsk, shortAsk, size, ttl, now,
					fmt.Sprintf("temporal_overlap buy_pair asset=%s long=%dm short=%dm sum_ask=%.4f edge_bps=%.1f",
						p.asset, p.longMinutes, p.shortMinutes, sumAsk, edge*10_000)), nil
			}
		}

		if longBid > 0 && shortBid > 0 {
			sumBid := longBid + shortBid
			edge := sumBid - 1.0
			if edge > minEdge {
				t.markEmitted(p.id, now)
				return temporalPairSignals(p, domain.OrderSideSell, longBid, shortBid, size, ttl, now,
					fmt.Sprintf("temporal_overlap sell_pair asset=%s long=%dm short=%dm sum_bid=%.4f edge_bps=%.1f",
						p.asset, p.longMinutes, p.shortMinutes, sumBid, edge*10_000)), nil
			}
		}
	}

	return nil, nil
}

func temporalPairSignals(
	p temporalPair,
	side domain.OrderSide,
	longPrice, shortPrice, size float64,
	ttl time.Duration,
	now time.Time,
	reason string,
) []domain.TradeSignal {
	legGroupID := uuid.New().String()
	return []domain.TradeSignal{
		{
			ID:         fmt.Sprintf("to-%s-long-%d", side, now.UnixNano()),
			Source:     "temporal_overlap",
			MarketID:   p.longMarketID,
			TokenID:    p.longTokenID,
			Side:       side,
			PriceTicks: int64(longPrice * 1e6),
			SizeUnits:  int64(size * 1e6),
			Urgency:    domain.SignalUrgencyImmediate,
			Reason:     reason,
			Metadata: map[string]string{
				"leg_group_id": legGroupID,
				"leg_count":    "2",
				"leg_policy":   string(domain.LegPolicyAllOrNone),
				"arb_type":     string(domain.ArbTypeCombinatorial),
			},
			CreatedAt: now,
			ExpiresAt: now.Add(ttl),
		},
		{
			ID:         fmt.Sprintf("to-%s-short-%d", side, now.UnixNano()),
			Source:     "temporal_overlap",
			MarketID:   p.shortMarketID,
			TokenID:    p.shortTokenID,
			Side:       side,
			PriceTicks: int64(shortPrice * 1e6),
			SizeUnits:  int64(size * 1e6),
			Urgency:    domain.SignalUrgencyImmediate,
			Reason:     reason,
			Metadata: map[string]string{
				"leg_group_id": legGroupID,
				"leg_count":    "2",
				"leg_policy":   string(domain.LegPolicyAllOrNone),
				"arb_type":     string(domain.ArbTypeCombinatorial),
			},
			CreatedAt: now,
			ExpiresAt: now.Add(ttl),
		},
	}
}

func (t *TemporalOverlap) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	t.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}

func (t *TemporalOverlap) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	t.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}

func (t *TemporalOverlap) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}

func (t *TemporalOverlap) Close() error { return nil }

func (t *TemporalOverlap) snapshotForToken(ctx context.Context, current domain.OrderbookSnapshot, tokenID string) (domain.OrderbookSnapshot, error) {
	if current.AssetID == tokenID {
		return current, nil
	}
	return t.books.GetSnapshot(ctx, tokenID)
}

func (t *TemporalOverlap) refreshPairs(ctx context.Context, now time.Time, logErrors bool) error {
	markets, err := t.markets.ListActive(ctx, domain.ListOpts{Limit: 600})
	if err != nil {
		if logErrors {
			t.logger.WarnContext(ctx, "temporal_overlap: list active markets failed", slog.String("error", err.Error()))
		}
		return err
	}
	descByAsset := map[string][]temporalDescriptor{}
	for _, m := range markets {
		d, ok := describeTemporalMarket(m)
		if !ok {
			continue
		}
		descByAsset[d.asset] = append(descByAsset[d.asset], d)
	}

	maxPairs := t.maxPairs()
	var pairs []temporalPair
	for asset, descs := range descByAsset {
		ups := make([]temporalDescriptor, 0, len(descs))
		downs := make([]temporalDescriptor, 0, len(descs))
		for _, d := range descs {
			if d.direction == "up" {
				ups = append(ups, d)
			}
			if d.direction == "down" {
				downs = append(downs, d)
			}
		}
		for _, up := range ups {
			for _, down := range downs {
				if up.minutes <= down.minutes {
					continue
				}
				pairs = append(pairs, temporalPair{
					id:            fmt.Sprintf("%s:%s", up.marketID, down.marketID),
					longMarketID:  up.marketID,
					longTokenID:   up.tokenID,
					longMinutes:   up.minutes,
					shortMarketID: down.marketID,
					shortTokenID:  down.tokenID,
					shortMinutes:  down.minutes,
					asset:         asset,
				})
				if len(pairs) >= maxPairs {
					break
				}
			}
			if len(pairs) >= maxPairs {
				break
			}
		}
		if len(pairs) >= maxPairs {
			break
		}
	}

	byToken := make(map[string][]temporalPair, len(pairs)*2)
	for _, p := range pairs {
		byToken[p.longTokenID] = append(byToken[p.longTokenID], p)
		byToken[p.shortTokenID] = append(byToken[p.shortTokenID], p)
	}

	t.mu.Lock()
	t.pairs = pairs
	t.byToken = byToken
	t.lastRefresh = now
	t.mu.Unlock()

	if len(pairs) > 0 {
		t.logger.DebugContext(ctx, "temporal_overlap: pairs refreshed", slog.Int("pairs", len(pairs)))
	}
	return nil
}

func describeTemporalMarket(m domain.Market) (temporalDescriptor, bool) {
	text := strings.ToLower(strings.TrimSpace(m.Question + " " + m.Slug))
	if text == "" || m.TokenIDs[0] == "" {
		return temporalDescriptor{}, false
	}
	minutes := extractMinutes(text)
	if minutes <= 0 {
		return temporalDescriptor{}, false
	}
	direction := ""
	switch {
	case strings.Contains(text, " up"), strings.Contains(text, "higher"), strings.Contains(text, " rise"), strings.Contains(text, " increase"):
		direction = "up"
	case strings.Contains(text, " down"), strings.Contains(text, "lower"), strings.Contains(text, " fall"), strings.Contains(text, " decrease"):
		direction = "down"
	default:
		return temporalDescriptor{}, false
	}
	asset := extractAsset(text)
	if asset == "" {
		return temporalDescriptor{}, false
	}
	return temporalDescriptor{
		marketID:  m.ID,
		tokenID:   m.TokenIDs[0],
		asset:     asset,
		direction: direction,
		minutes:   minutes,
	}, true
}

func extractMinutes(text string) int {
	m := temporalMinutesRE.FindStringSubmatch(text)
	if len(m) < 2 {
		return 0
	}
	v := 0
	for _, ch := range m[1] {
		v = v*10 + int(ch-'0')
	}
	return v
}

func extractAsset(text string) string {
	switch {
	case strings.Contains(text, "btc"), strings.Contains(text, "bitcoin"):
		return "btc"
	case strings.Contains(text, "eth"), strings.Contains(text, "ethereum"):
		return "eth"
	case strings.Contains(text, "sol"), strings.Contains(text, "solana"):
		return "sol"
	case strings.Contains(text, "doge"):
		return "doge"
	default:
		return ""
	}
}

func (t *TemporalOverlap) recentlyEmitted(pairID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	last, ok := t.lastEmit[pairID]
	if !ok {
		return false
	}
	return now.Sub(last) < time.Duration(t.cooldownSec())*time.Second
}

func (t *TemporalOverlap) markEmitted(pairID string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastEmit[pairID] = now
}

func (t *TemporalOverlap) minEdgeBps() int {
	if v, ok := t.cfg.Params["min_edge_bps"].(int); ok {
		return v
	}
	if v, ok := t.cfg.Params["min_edge_bps"].(int64); ok {
		return int(v)
	}
	if v, ok := t.cfg.Params["min_edge_bps"].(float64); ok {
		return int(v)
	}
	return defaultTemporalMinEdgeBps
}

func (t *TemporalOverlap) sizePerLeg() float64 {
	if v, ok := t.cfg.Params["size_per_leg"].(float64); ok {
		return v
	}
	if v, ok := t.cfg.Params["size_per_leg"].(int); ok {
		return float64(v)
	}
	if v, ok := t.cfg.Params["size_per_leg"].(int64); ok {
		return float64(v)
	}
	return defaultTemporalSizePerLeg
}

func (t *TemporalOverlap) ttlSeconds() int {
	if v, ok := t.cfg.Params["ttl_seconds"].(int); ok {
		return v
	}
	if v, ok := t.cfg.Params["ttl_seconds"].(int64); ok {
		return int(v)
	}
	if v, ok := t.cfg.Params["ttl_seconds"].(float64); ok {
		return int(v)
	}
	return defaultTemporalTTLSeconds
}

func (t *TemporalOverlap) maxStaleSec() int {
	if v, ok := t.cfg.Params["max_stale_sec"].(int); ok {
		return v
	}
	if v, ok := t.cfg.Params["max_stale_sec"].(int64); ok {
		return int(v)
	}
	if v, ok := t.cfg.Params["max_stale_sec"].(float64); ok {
		return int(v)
	}
	return defaultTemporalMaxStaleSec
}

func (t *TemporalOverlap) cooldownSec() int {
	if v, ok := t.cfg.Params["cooldown_sec"].(int); ok {
		return v
	}
	if v, ok := t.cfg.Params["cooldown_sec"].(int64); ok {
		return int(v)
	}
	if v, ok := t.cfg.Params["cooldown_sec"].(float64); ok {
		return int(v)
	}
	return defaultTemporalCooldownSec
}

func (t *TemporalOverlap) refreshMinutes() int {
	if v, ok := t.cfg.Params["refresh_minutes"].(int); ok {
		return v
	}
	if v, ok := t.cfg.Params["refresh_minutes"].(int64); ok {
		return int(v)
	}
	if v, ok := t.cfg.Params["refresh_minutes"].(float64); ok {
		return int(v)
	}
	return defaultTemporalRefreshMins
}

func (t *TemporalOverlap) maxPairs() int {
	if v, ok := t.cfg.Params["max_pairs"].(int); ok {
		return v
	}
	if v, ok := t.cfg.Params["max_pairs"].(int64); ok {
		return int(v)
	}
	if v, ok := t.cfg.Params["max_pairs"].(float64); ok {
		return int(v)
	}
	return defaultTemporalMaxPairs
}
