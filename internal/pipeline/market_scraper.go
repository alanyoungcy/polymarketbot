package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// MarketSyncer persists a batch of markets to the store.
type MarketSyncer interface {
	SyncMarkets(ctx context.Context, markets []domain.Market) error
}

// MarketFetcher retrieves markets from an external API.
type MarketFetcher interface {
	GetMarkets(ctx context.Context, limit, offset int) ([]domain.Market, error)
}

// MarketScraper scrapes market data from external APIs and syncs to the store.
type MarketScraper struct {
	marketSvc MarketSyncer
	fetcher   MarketFetcher
	logger    *slog.Logger
}

// NewMarketScraper creates a new MarketScraper.
func NewMarketScraper(syncer MarketSyncer, fetcher MarketFetcher, logger *slog.Logger) *MarketScraper {
	return &MarketScraper{
		marketSvc: syncer,
		fetcher:   fetcher,
		logger:    logger,
	}
}

// Run executes a single scrape run that paginates through all markets and syncs
// each batch to the store.
func (s *MarketScraper) Run(ctx context.Context) error {
	const pageSize = 100
	offset := 0
	totalSynced := 0

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("market scraper context cancelled: %w", err)
		}

		markets, err := s.fetcher.GetMarkets(ctx, pageSize, offset)
		if err != nil {
			return fmt.Errorf("fetching markets at offset %d: %w", offset, err)
		}

		if len(markets) == 0 {
			break
		}

		if err := s.marketSvc.SyncMarkets(ctx, markets); err != nil {
			return fmt.Errorf("syncing %d markets at offset %d: %w", len(markets), offset, err)
		}

		totalSynced += len(markets)
		s.logger.Info("synced market batch",
			slog.Int("batch_size", len(markets)),
			slog.Int("total_synced", totalSynced),
			slog.Int("offset", offset),
		)

		if len(markets) < pageSize {
			break
		}

		offset += pageSize
	}

	s.logger.Info("market scrape complete", slog.Int("total_synced", totalSynced))
	return nil
}

// RunLoop runs the market scraper on a repeating interval until the context is
// cancelled.
func (s *MarketScraper) RunLoop(ctx context.Context, interval time.Duration) error {
	// Run immediately on start.
	if err := s.Run(ctx); err != nil {
		s.logger.Error("market scrape failed", slog.String("error", err.Error()))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("market scraper loop stopped")
			return ctx.Err()
		case <-ticker.C:
			if err := s.Run(ctx); err != nil {
				s.logger.Error("market scrape failed", slog.String("error", err.Error()))
			}
		}
	}
}
