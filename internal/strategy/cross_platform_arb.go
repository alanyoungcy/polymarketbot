package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/platform/kalshi"
)

const (
	defaultCrossMinEdgeBps = 60
	defaultCrossSizePerLeg = 5.0
	defaultCrossTTLSeconds = 30
	defaultCrossRefreshSec = 5
	defaultCrossMaxStale   = 8
	defaultCrossCooldown   = 3
)

// KalshiMarketGetter fetches a Kalshi market quote.
type KalshiMarketGetter interface {
	GetMarket(ctx context.Context, ticker string) (kalshi.KalshiMarket, error)
}

type kalshiQuote struct {
	yesAsk float64
	yesBid float64
	noAsk  float64
	noBid  float64
	at     time.Time
}

// CrossPlatformArb detects Polymarket/Kalshi pricing gaps and emits the
// Polymarket leg as executable signal.
type CrossPlatformArb struct {
	cfg     Config
	tracker *PriceTracker
	markets domain.MarketStore
	books   domain.OrderbookCache
	kalshi  KalshiMarketGetter
	logger  *slog.Logger

	marketMap map[string]string // poly market ID (or slug) -> kalshi ticker

	mu       sync.Mutex
	quotes   map[string]kalshiQuote // ticker -> quote
	lastEmit map[string]time.Time   // poly market ID -> last signal
}

// NewCrossPlatformArb creates a cross-platform strategy.
func NewCrossPlatformArb(
	cfg Config,
	tracker *PriceTracker,
	markets domain.MarketStore,
	books domain.OrderbookCache,
	kalshiClient KalshiMarketGetter,
	marketMap map[string]string,
	logger *slog.Logger,
) *CrossPlatformArb {
	cp := &CrossPlatformArb{
		cfg:       cfg,
		tracker:   tracker,
		markets:   markets,
		books:     books,
		kalshi:    kalshiClient,
		logger:    logger.With(slog.String("strategy", "cross_platform_arb")),
		marketMap: make(map[string]string, len(marketMap)),
		quotes:    make(map[string]kalshiQuote),
		lastEmit:  make(map[string]time.Time),
	}
	for k, v := range marketMap {
		cp.marketMap[k] = v
	}
	return cp
}

// Name returns the strategy identifier.
func (c *CrossPlatformArb) Name() string { return "cross_platform_arb" }

// Init is a no-op.
func (c *CrossPlatformArb) Init(_ context.Context) error { return nil }

// OnBookUpdate compares polymarket YES/NO prices against Kalshi YES/NO prices.
func (c *CrossPlatformArb) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	if c.markets == nil || c.books == nil || c.kalshi == nil {
		return nil, nil
	}

	mkt, err := c.markets.GetByTokenID(ctx, snap.AssetID)
	if err != nil {
		return nil, nil
	}
	ticker := c.mapTicker(mkt.ID, mkt.Slug)
	if ticker == "" {
		return nil, nil
	}

	now := time.Now().UTC()
	if c.recentlyEmitted(mkt.ID, now) {
		return nil, nil
	}

	yesToken, noToken := mkt.TokenIDs[0], mkt.TokenIDs[1]
	if yesToken == "" || noToken == "" {
		return nil, nil
	}
	maxStale := time.Duration(c.maxStaleSec()) * time.Second
	yesSnap, err := c.snapshotForToken(ctx, snap, yesToken)
	if err != nil || yesSnap.AssetID == "" || now.Sub(yesSnap.Timestamp) > maxStale {
		return nil, nil
	}
	noSnap, err := c.snapshotForToken(ctx, snap, noToken)
	if err != nil || noSnap.AssetID == "" || now.Sub(noSnap.Timestamp) > maxStale {
		return nil, nil
	}
	polyYesAsk, polyYesBid := bestAsk(yesSnap), bestBid(yesSnap)
	polyNoAsk, polyNoBid := bestAsk(noSnap), bestBid(noSnap)
	if polyYesAsk <= 0 && polyNoAsk <= 0 && polyYesBid <= 0 && polyNoBid <= 0 {
		return nil, nil
	}

	quote, err := c.getKalshiQuote(ctx, ticker, now)
	if err != nil {
		c.logger.DebugContext(ctx, "cross_platform_arb: kalshi quote unavailable",
			slog.String("ticker", ticker),
			slog.String("error", err.Error()),
		)
		return nil, nil
	}
	minEdge := float64(c.minEdgeBps()) / 10_000

	type candidate struct {
		tokenID string
		side    domain.OrderSide
		price   float64
		edge    float64
		reason  string
	}
	var best candidate

	// Buy Poly YES + Buy Kalshi NO.
	if polyYesAsk > 0 && quote.noAsk > 0 {
		edge := 1.0 - (polyYesAsk + quote.noAsk)
		if edge > minEdge && edge > best.edge {
			best = candidate{
				tokenID: yesToken,
				side:    domain.OrderSideBuy,
				price:   polyYesAsk,
				edge:    edge,
				reason:  fmt.Sprintf("cross_platform_arb poly_yes+kalshi_no edge_bps=%.1f", edge*10_000),
			}
		}
	}

	// Buy Poly NO + Buy Kalshi YES.
	if polyNoAsk > 0 && quote.yesAsk > 0 {
		edge := 1.0 - (polyNoAsk + quote.yesAsk)
		if edge > minEdge && edge > best.edge {
			best = candidate{
				tokenID: noToken,
				side:    domain.OrderSideBuy,
				price:   polyNoAsk,
				edge:    edge,
				reason:  fmt.Sprintf("cross_platform_arb poly_no+kalshi_yes edge_bps=%.1f", edge*10_000),
			}
		}
	}

	// Sell Poly YES vs Sell Kalshi NO.
	if polyYesBid > 0 && quote.noBid > 0 {
		edge := (polyYesBid + quote.noBid) - 1.0
		if edge > minEdge && edge > best.edge {
			best = candidate{
				tokenID: yesToken,
				side:    domain.OrderSideSell,
				price:   polyYesBid,
				edge:    edge,
				reason:  fmt.Sprintf("cross_platform_arb sell_poly_yes_vs_kalshi_no edge_bps=%.1f", edge*10_000),
			}
		}
	}

	// Sell Poly NO vs Sell Kalshi YES.
	if polyNoBid > 0 && quote.yesBid > 0 {
		edge := (polyNoBid + quote.yesBid) - 1.0
		if edge > minEdge && edge > best.edge {
			best = candidate{
				tokenID: noToken,
				side:    domain.OrderSideSell,
				price:   polyNoBid,
				edge:    edge,
				reason:  fmt.Sprintf("cross_platform_arb sell_poly_no_vs_kalshi_yes edge_bps=%.1f", edge*10_000),
			}
		}
	}

	if best.edge <= 0 || best.tokenID == "" || best.price <= 0 {
		return nil, nil
	}

	c.markEmitted(mkt.ID, now)
	ttl := time.Duration(c.ttlSeconds()) * time.Second
	sizePerLeg := c.sizePerLeg()
	sig := domain.TradeSignal{
		ID:         fmt.Sprintf("cp-%s-%d", mkt.ID, now.UnixNano()),
		Source:     c.Name(),
		MarketID:   mkt.ID,
		TokenID:    best.tokenID,
		Side:       best.side,
		PriceTicks: int64(best.price * 1e6),
		SizeUnits:  int64(sizePerLeg * 1e6),
		Urgency:    domain.SignalUrgencyHigh,
		Reason:     best.reason,
		Metadata: map[string]string{
			"kalshi_ticker": ticker,
			"arb_type":      string(domain.ArbTypeCrossPlatform),
		},
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	return []domain.TradeSignal{sig}, nil
}

func (c *CrossPlatformArb) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	c.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}

func (c *CrossPlatformArb) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	c.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}

func (c *CrossPlatformArb) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}

func (c *CrossPlatformArb) Close() error { return nil }

func (c *CrossPlatformArb) snapshotForToken(ctx context.Context, current domain.OrderbookSnapshot, tokenID string) (domain.OrderbookSnapshot, error) {
	if current.AssetID == tokenID {
		return current, nil
	}
	return c.books.GetSnapshot(ctx, tokenID)
}

func (c *CrossPlatformArb) mapTicker(marketID, slug string) string {
	if v := c.marketMap[marketID]; v != "" {
		return v
	}
	return c.marketMap[slug]
}

func (c *CrossPlatformArb) getKalshiQuote(ctx context.Context, ticker string, now time.Time) (kalshiQuote, error) {
	c.mu.Lock()
	cached, ok := c.quotes[ticker]
	c.mu.Unlock()

	refreshTTL := time.Duration(c.refreshSec()) * time.Second
	if ok && now.Sub(cached.at) <= refreshTTL {
		return cached, nil
	}

	m, err := c.kalshi.GetMarket(ctx, ticker)
	if err != nil {
		return kalshiQuote{}, err
	}
	q := kalshiQuote{
		yesAsk: normalizeProb(m.YesAsk),
		yesBid: normalizeProb(m.YesBid),
		noAsk:  normalizeProb(m.NoAsk),
		noBid:  normalizeProb(m.NoBid),
		at:     now,
	}
	c.mu.Lock()
	c.quotes[ticker] = q
	c.mu.Unlock()
	return q, nil
}

func normalizeProb(v float64) float64 {
	if v <= 0 {
		return 0
	}
	// Kalshi API values are typically cents (0..100); normalize to 0..1.
	if v > 1.0 {
		return v / 100.0
	}
	return v
}

func (c *CrossPlatformArb) recentlyEmitted(marketID string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	last, ok := c.lastEmit[marketID]
	if !ok {
		return false
	}
	return now.Sub(last) < time.Duration(c.cooldownSec())*time.Second
}

func (c *CrossPlatformArb) markEmitted(marketID string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastEmit[marketID] = now
}

func (c *CrossPlatformArb) minEdgeBps() int {
	if v, ok := c.cfg.Params["min_edge_bps"].(int); ok {
		return v
	}
	if v, ok := c.cfg.Params["min_edge_bps"].(int64); ok {
		return int(v)
	}
	if v, ok := c.cfg.Params["min_edge_bps"].(float64); ok {
		return int(v)
	}
	return defaultCrossMinEdgeBps
}

func (c *CrossPlatformArb) sizePerLeg() float64 {
	if v, ok := c.cfg.Params["size_per_leg"].(float64); ok {
		return v
	}
	if v, ok := c.cfg.Params["size_per_leg"].(int); ok {
		return float64(v)
	}
	if v, ok := c.cfg.Params["size_per_leg"].(int64); ok {
		return float64(v)
	}
	return defaultCrossSizePerLeg
}

func (c *CrossPlatformArb) ttlSeconds() int {
	if v, ok := c.cfg.Params["ttl_seconds"].(int); ok {
		return v
	}
	if v, ok := c.cfg.Params["ttl_seconds"].(int64); ok {
		return int(v)
	}
	if v, ok := c.cfg.Params["ttl_seconds"].(float64); ok {
		return int(v)
	}
	return defaultCrossTTLSeconds
}

func (c *CrossPlatformArb) refreshSec() int {
	if v, ok := c.cfg.Params["refresh_sec"].(int); ok {
		return v
	}
	if v, ok := c.cfg.Params["refresh_sec"].(int64); ok {
		return int(v)
	}
	if v, ok := c.cfg.Params["refresh_sec"].(float64); ok {
		return int(v)
	}
	return defaultCrossRefreshSec
}

func (c *CrossPlatformArb) maxStaleSec() int {
	if v, ok := c.cfg.Params["max_stale_sec"].(int); ok {
		return v
	}
	if v, ok := c.cfg.Params["max_stale_sec"].(int64); ok {
		return int(v)
	}
	if v, ok := c.cfg.Params["max_stale_sec"].(float64); ok {
		return int(v)
	}
	return defaultCrossMaxStale
}

func (c *CrossPlatformArb) cooldownSec() int {
	if v, ok := c.cfg.Params["cooldown_sec"].(int); ok {
		return v
	}
	if v, ok := c.cfg.Params["cooldown_sec"].(int64); ok {
		return int(v)
	}
	if v, ok := c.cfg.Params["cooldown_sec"].(float64); ok {
		return int(v)
	}
	return defaultCrossCooldown
}
