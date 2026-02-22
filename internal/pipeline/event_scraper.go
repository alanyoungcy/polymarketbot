package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/platform/polymarket"
)

// EventFetcher retrieves events from the Gamma API.
type EventFetcher interface {
	GetEvents(ctx context.Context, limit, offset int) ([]polymarket.APIEvent, error)
}

// EventScraper scrapes event data from the Gamma API and syncs condition
// groups and their market links to the store. If marketStore is set, it
// upserts each market before linking so condition_group_markets.market_id
// satisfies the foreign key to markets(id).
type EventScraper struct {
	groups      domain.ConditionGroupStore
	marketStore domain.MarketStore
	fetcher     EventFetcher
	logger      *slog.Logger
}

// NewEventScraper creates a new EventScraper. Pass a non-nil marketStore to
// upsert markets before linking (avoids FK violations when the market scraper
// has not yet ingested those markets).
func NewEventScraper(groups domain.ConditionGroupStore, fetcher EventFetcher, logger *slog.Logger, marketStore domain.MarketStore) *EventScraper {
	return &EventScraper{
		groups:      groups,
		marketStore: marketStore,
		fetcher:     fetcher,
		logger:      logger,
	}
}

// Run executes a single scrape run that paginates through all events and
// upserts each as a ConditionGroup with linked markets.
func (s *EventScraper) Run(ctx context.Context) error {
	const pageSize = 100
	offset := 0
	totalSynced := 0

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("event scraper context cancelled: %w", err)
		}

		events, err := s.fetcher.GetEvents(ctx, pageSize, offset)
		if err != nil {
			return fmt.Errorf("fetching events at offset %d: %w", offset, err)
		}

		if len(events) == 0 {
			break
		}

		for i := range events {
			cg := events[i].ToDomainConditionGroup()
			if err := s.groups.Upsert(ctx, cg); err != nil {
				if !errors.Is(err, context.Canceled) {
					s.logger.Error("event scraper: upsert condition group failed",
						slog.String("group_id", cg.ID),
						slog.String("error", err.Error()),
					)
				}
				continue
			}

			for _, mkt := range events[i].Markets {
				if mkt.ID == "" {
					continue
				}
				if s.marketStore != nil {
					if err := s.marketStore.Upsert(ctx, mkt.ToDomainMarket()); err != nil {
						if !errors.Is(err, context.Canceled) {
							s.logger.Error("event scraper: upsert market failed",
								slog.String("market_id", mkt.ID),
								slog.String("error", err.Error()),
							)
						}
						continue
					}
				}
				if err := s.groups.LinkMarket(ctx, cg.ID, mkt.ID); err != nil {
					if !errors.Is(err, context.Canceled) {
						s.logger.Error("event scraper: link market failed",
							slog.String("group_id", cg.ID),
							slog.String("market_id", mkt.ID),
							slog.String("error", err.Error()),
						)
					}
				}
			}
		}

		totalSynced += len(events)
		s.logger.Info("synced event batch",
			slog.Int("batch_size", len(events)),
			slog.Int("total_synced", totalSynced),
			slog.Int("offset", offset),
		)

		if len(events) < pageSize {
			break
		}

		offset += pageSize
	}

	s.logger.Info("event scrape complete", slog.Int("total_synced", totalSynced))
	return nil
}

// RunLoop runs the event scraper on a repeating interval until the context is
// cancelled.
func (s *EventScraper) RunLoop(ctx context.Context, interval time.Duration) error {
	// Run immediately on start.
	if err := s.Run(ctx); err != nil {
		s.logger.Error("event scrape failed", slog.String("error", err.Error()))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("event scraper loop stopped")
			return ctx.Err()
		case <-ticker.C:
			if err := s.Run(ctx); err != nil {
				s.logger.Error("event scrape failed", slog.String("error", err.Error()))
			}
		}
	}
}
