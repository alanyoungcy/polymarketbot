package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// MarketService handles market discovery and metadata sync.
type MarketService struct {
	markets domain.MarketStore
	cache   domain.MarketCache
	bus     domain.SignalBus
	logger  *slog.Logger
}

// NewMarketService creates a MarketService with all required dependencies.
func NewMarketService(
	markets domain.MarketStore,
	cache domain.MarketCache,
	bus domain.SignalBus,
	logger *slog.Logger,
) *MarketService {
	return &MarketService{
		markets: markets,
		cache:   cache,
		bus:     bus,
		logger:  logger,
	}
}

// SyncMarkets upserts a batch of markets into the persistent store and
// invalidates cached entries so subsequent reads pick up fresh data.
func (s *MarketService) SyncMarkets(ctx context.Context, markets []domain.Market) error {
	if len(markets) == 0 {
		return nil
	}

	if err := s.markets.UpsertBatch(ctx, markets); err != nil {
		return fmt.Errorf("market_service: upsert batch: %w", err)
	}

	// Invalidate cache entries for every synced market.
	for _, m := range markets {
		if err := s.cache.Invalidate(ctx, m.ID); err != nil {
			s.logger.WarnContext(ctx, "market_service: cache invalidate failed",
				slog.String("market_id", m.ID),
				slog.String("error", err.Error()),
			)
			// Non-fatal: the cache will eventually expire on its own.
		}
	}

	s.logger.InfoContext(ctx, "market_service: synced markets",
		slog.Int("count", len(markets)),
	)

	return nil
}

// GetMarket retrieves a market by ID, checking the cache first and falling
// back to the persistent store on a cache miss.
func (s *MarketService) GetMarket(ctx context.Context, id string) (domain.Market, error) {
	// Try the cache first.
	m, err := s.cache.Get(ctx, id)
	if err == nil {
		return m, nil
	}

	// Cache miss or error -- fall through to store.
	m, err = s.markets.GetByID(ctx, id)
	if err != nil {
		return domain.Market{}, fmt.Errorf("market_service: get by id %q: %w", id, err)
	}

	// Back-fill cache; log but do not fail on cache write errors.
	if cacheErr := s.cache.Set(ctx, m); cacheErr != nil {
		s.logger.WarnContext(ctx, "market_service: cache set failed",
			slog.String("market_id", id),
			slog.String("error", cacheErr.Error()),
		)
	}

	return m, nil
}

// GetMarketByToken retrieves a market by one of its ERC-1155 token IDs,
// checking the cache first and falling back to the persistent store.
func (s *MarketService) GetMarketByToken(ctx context.Context, tokenID string) (domain.Market, error) {
	// Try the cache first.
	m, err := s.cache.GetByToken(ctx, tokenID)
	if err == nil {
		return m, nil
	}

	// Cache miss or error -- fall through to store.
	m, err = s.markets.GetByTokenID(ctx, tokenID)
	if err != nil {
		return domain.Market{}, fmt.Errorf("market_service: get by token %q: %w", tokenID, err)
	}

	// Back-fill cache.
	if cacheErr := s.cache.Set(ctx, m); cacheErr != nil {
		s.logger.WarnContext(ctx, "market_service: cache set failed",
			slog.String("token_id", tokenID),
			slog.String("error", cacheErr.Error()),
		)
	}

	return m, nil
}

// ListActive returns active markets directly from the persistent store.
func (s *MarketService) ListActive(ctx context.Context, opts domain.ListOpts) ([]domain.Market, error) {
	markets, err := s.markets.ListActive(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("market_service: list active: %w", err)
	}
	return markets, nil
}

// Count returns the total number of markets in the persistent store.
func (s *MarketService) Count(ctx context.Context) (int64, error) {
	count, err := s.markets.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("market_service: count: %w", err)
	}
	return count, nil
}
