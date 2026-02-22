package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/platform/polymarket"
)

// RewardsTracker queries the Gamma API for LP/reward-eligible markets and
// caches the list for the liquidity_provider strategy.
type RewardsTracker struct {
	gamma       *polymarket.GammaClient
	minVolume   float64
	cacheTTL    time.Duration
	lastRefresh  time.Time
	cached      []string // eligible market IDs
	mu          sync.RWMutex
	logger      *slog.Logger
}

// NewRewardsTracker creates a RewardsTracker. minVolume is minimum daily volume (USD) for a market to be eligible.
func NewRewardsTracker(gamma *polymarket.GammaClient, minVolume float64, logger *slog.Logger) *RewardsTracker {
	if minVolume <= 0 {
		minVolume = 50_000
	}
	return &RewardsTracker{
		gamma:     gamma,
		minVolume: minVolume,
		cacheTTL:  10 * time.Minute,
		logger:    logger.With(slog.String("component", "rewards_tracker")),
	}
}

// EligibleMarketIDs returns market IDs that are eligible for LP rewards. Results are cached and refreshed periodically.
func (r *RewardsTracker) EligibleMarketIDs(ctx context.Context) ([]string, error) {
	r.mu.RLock()
	if len(r.cached) > 0 && time.Since(r.lastRefresh) < r.cacheTTL {
		ids := make([]string, len(r.cached))
		copy(ids, r.cached)
		r.mu.RUnlock()
		return ids, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock
	if len(r.cached) > 0 && time.Since(r.lastRefresh) < r.cacheTTL {
		ids := make([]string, len(r.cached))
		copy(ids, r.cached)
		return ids, nil
	}

	markets, err := r.gamma.ListRewardEligibleMarkets(ctx, r.minVolume, 200)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(markets))
	for _, m := range markets {
		ids = append(ids, m.MarketID)
	}
	r.cached = ids
	r.lastRefresh = time.Now()
	r.logger.DebugContext(ctx, "rewards eligible markets refreshed", slog.Int("count", len(ids)))
	return ids, nil
}

// SetMinVolume updates the minimum volume filter (e.g. from strategy config).
func (r *RewardsTracker) SetMinVolume(v float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.minVolume = v
	r.cached = nil
	r.lastRefresh = time.Time{}
}

// RewardEstimate holds estimated reward for a market (placeholder for future API).
type RewardEstimate struct {
	MarketID string
	Estimate float64 // USD or points
}

// EstimatesPerMarket returns reward estimates per market; currently returns empty slice.
// Reserved for when Polymarket exposes reward accrual API.
func (r *RewardsTracker) EstimatesPerMarket(ctx context.Context) ([]RewardEstimate, error) {
	_ = ctx
	return nil, nil
}
