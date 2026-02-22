package strategy

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

const (
	defaultHalfSpreadBps   = 50
	defaultRequoteThreshold = 0.005
	defaultLPSize          = 10.0
	defaultMaxMarkets      = 5
	defaultLPMinVolume     = 50_000
)

// QuotePair holds last quoted bid/ask for a market (for requote logic).
type QuotePair struct {
	MarketID    string
	BidPrice    float64
	AskPrice    float64
	LastMid     float64
	LastQuoteAt time.Time
}

// LiquidityProvider places and maintains bid/ask quotes on eligible markets.
type LiquidityProvider struct {
	cfg          Config
	tracker      *PriceTracker
	rewards      RewardsTracker
	markets      domain.MarketStore
	activeQuotes map[string]*QuotePair // keyed by token (asset) ID
	mu           sync.RWMutex
	logger       *slog.Logger
}

// RewardsTracker is the service that provides eligible market IDs (injected to avoid circular import).
type RewardsTracker interface {
	EligibleMarketIDs(ctx context.Context) ([]string, error)
}

// NewLiquidityProvider creates a LiquidityProvider. rewards can be nil; then no markets are pre-selected.
func NewLiquidityProvider(cfg Config, tracker *PriceTracker, rewards RewardsTracker, markets domain.MarketStore, logger *slog.Logger) *LiquidityProvider {
	return &LiquidityProvider{
		cfg:          cfg,
		tracker:      tracker,
		rewards:      rewards,
		markets:      markets,
		activeQuotes: make(map[string]*QuotePair),
		logger:       logger.With(slog.String("strategy", "liquidity_provider")),
	}
}

// Name returns the strategy identifier.
func (lp *LiquidityProvider) Name() string { return "liquidity_provider" }

// Init loads eligible markets and seeds activeQuotes (by token ID) up to max_markets.
func (lp *LiquidityProvider) Init(ctx context.Context) error {
	if lp.rewards == nil || lp.markets == nil {
		return nil
	}
	marketIDs, err := lp.rewards.EligibleMarketIDs(ctx)
	if err != nil {
		return err
	}
	max := lp.maxMarkets()
	lp.mu.Lock()
	for i, mid := range marketIDs {
		if i >= max {
			break
		}
		mkt, err := lp.markets.GetByID(ctx, mid)
		if err != nil {
			continue
		}
		yesTokenID := mkt.TokenIDs[0]
		lp.activeQuotes[yesTokenID] = &QuotePair{MarketID: mid}
	}
	lp.mu.Unlock()
	return nil
}

// OnBookUpdate requotes when mid moves beyond threshold.
func (lp *LiquidityProvider) OnBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.TradeSignal, error) {
	mid := snap.MidPrice
	if mid <= 0 && snap.BestBid > 0 && snap.BestAsk > 0 {
		mid = (snap.BestBid + snap.BestAsk) / 2
	}
	if mid <= 0 {
		return nil, nil
	}
	lp.mu.Lock()
	q, ok := lp.activeQuotes[snap.AssetID]
	if !ok {
		lp.mu.Unlock()
		return nil, nil
	}
	threshold := lp.requoteThreshold()
	shouldQuote := !q.LastQuoteAt.IsZero() && (q.LastMid < 1e-9 || (mid-q.LastMid > threshold || q.LastMid-mid > threshold))
	if q.LastQuoteAt.IsZero() {
		shouldQuote = true
	}
	if !shouldQuote {
		lp.mu.Unlock()
		return nil, nil
	}
	halfSpread := float64(lp.halfSpreadBps()) / 10_000
	bidPrice := mid - halfSpread
	askPrice := mid + halfSpread
	if bidPrice < 0 {
		bidPrice = 0
	}
	if askPrice > 1 {
		askPrice = 1
	}
	q.BidPrice = bidPrice
	q.AskPrice = askPrice
	q.LastMid = mid
	q.LastQuoteAt = time.Now().UTC()
	lp.mu.Unlock()

	size := lp.size()
	now := time.Now().UTC()
	sigID := fmt.Sprintf("lp-%s-%d", snap.AssetID, now.UnixNano())
	signals := []domain.TradeSignal{
		{
			ID:         sigID + "-bid",
			Source:     lp.Name(),
			MarketID:   "",
			TokenID:    snap.AssetID,
			Side:       domain.OrderSideBuy,
			PriceTicks: int64(bidPrice * 1e6),
			SizeUnits:  int64(size * 1e6),
			Urgency:    domain.SignalUrgencyMedium,
			Reason:     "liquidity_provider bid",
			CreatedAt:  now,
			ExpiresAt:  now.Add(2 * time.Minute),
		},
		{
			ID:         sigID + "-ask",
			Source:     lp.Name(),
			MarketID:   "",
			TokenID:    snap.AssetID,
			Side:       domain.OrderSideSell,
			PriceTicks: int64(askPrice * 1e6),
			SizeUnits:  int64(size * 1e6),
			Urgency:    domain.SignalUrgencyMedium,
			Reason:     "liquidity_provider ask",
			CreatedAt:  now,
			ExpiresAt:  now.Add(2 * time.Minute),
		},
	}
	return signals, nil
}

func (lp *LiquidityProvider) OnPriceChange(_ context.Context, change domain.PriceChange) ([]domain.TradeSignal, error) {
	lp.tracker.Track(change.AssetID, change.Price, change.Timestamp)
	return nil, nil
}
func (lp *LiquidityProvider) OnTrade(_ context.Context, trade domain.Trade) ([]domain.TradeSignal, error) {
	lp.tracker.Track(trade.MarketID, trade.Price, trade.Timestamp)
	return nil, nil
}
func (lp *LiquidityProvider) OnSignal(_ context.Context, _ domain.TradeSignal) ([]domain.TradeSignal, error) {
	return nil, nil
}
func (lp *LiquidityProvider) Close() error { return nil }

func (lp *LiquidityProvider) halfSpreadBps() int {
	if v, ok := lp.cfg.Params["half_spread_bps"].(int); ok {
		return v
	}
	if v, ok := lp.cfg.Params["half_spread_bps"].(int64); ok {
		return int(v)
	}
	return defaultHalfSpreadBps
}
func (lp *LiquidityProvider) requoteThreshold() float64 {
	if v, ok := lp.cfg.Params["requote_threshold"].(float64); ok {
		return v
	}
	return defaultRequoteThreshold
}
func (lp *LiquidityProvider) size() float64 {
	if v, ok := lp.cfg.Params["size"].(float64); ok {
		return v
	}
	return defaultLPSize
}
func (lp *LiquidityProvider) maxMarkets() int {
	if v, ok := lp.cfg.Params["max_markets"].(int); ok {
		return v
	}
	if v, ok := lp.cfg.Params["max_markets"].(int64); ok {
		return int(v)
	}
	return defaultMaxMarkets
}
