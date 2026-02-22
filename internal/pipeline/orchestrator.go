package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"
)

// Orchestrator manages all pipeline goroutines: market scraping, Goldsky
// scraping, trade processing, and cold-storage archival.
type Orchestrator struct {
	marketScraper  *MarketScraper
	goldskyScraper *GoldskyScraper
	tradeProcessor *TradeProcessor
	archiver       *Archiver
	scrapeInterval time.Duration
	archiveCron    string
	logger         *slog.Logger
}

// NewOrchestrator creates a new Orchestrator that coordinates all pipeline
// sub-systems.
func NewOrchestrator(
	marketScraper *MarketScraper,
	goldskyScraper *GoldskyScraper,
	tradeProcessor *TradeProcessor,
	archiver *Archiver,
	scrapeInterval time.Duration,
	archiveCron string,
	logger *slog.Logger,
) *Orchestrator {
	return &Orchestrator{
		marketScraper:  marketScraper,
		goldskyScraper: goldskyScraper,
		tradeProcessor: tradeProcessor,
		archiver:       archiver,
		scrapeInterval: scrapeInterval,
		archiveCron:    archiveCron,
		logger:         logger,
	}
}

// Run starts all sub-pipelines as concurrent goroutines using an errgroup. Each
// goroutine respects ctx cancellation. If any goroutine returns a non-context
// error, the errgroup cancels the shared context and Run returns that error.
func (o *Orchestrator) Run(ctx context.Context) error {
	o.logger.Info("pipeline orchestrator starting",
		slog.Duration("scrape_interval", o.scrapeInterval),
		slog.String("archive_cron", o.archiveCron),
	)

	g, ctx := errgroup.WithContext(ctx)

	// 1. Market scraper on ticker.
	g.Go(func() error {
		o.logger.Info("starting market scraper loop")
		err := o.marketScraper.RunLoop(ctx, o.scrapeInterval)
		if ctx.Err() != nil {
			return nil // clean shutdown
		}
		return fmt.Errorf("market scraper: %w", err)
	})

	// 2. Goldsky scraper on ticker with continuous trade processing.
	g.Go(func() error {
		o.logger.Info("starting goldsky scraper + trade processor loop")
		err := o.runGoldskyAndProcess(ctx)
		if ctx.Err() != nil {
			return nil // clean shutdown
		}
		return fmt.Errorf("goldsky pipeline: %w", err)
	})

	// 3. Archiver on cron schedule.
	g.Go(func() error {
		o.logger.Info("starting archiver cron")
		err := o.archiver.RunCron(ctx, o.archiveCron)
		if ctx.Err() != nil {
			return nil // clean shutdown
		}
		return fmt.Errorf("archiver: %w", err)
	})

	err := g.Wait()
	if err != nil {
		o.logger.Error("pipeline orchestrator stopped with error", slog.String("error", err.Error()))
		return err
	}

	o.logger.Info("pipeline orchestrator stopped cleanly")
	return nil
}

// runGoldskyAndProcess combines the Goldsky scraper and trade processor into a
// single loop. On each tick it scrapes new fills and immediately processes them
// into enriched trades.
func (o *Orchestrator) runGoldskyAndProcess(ctx context.Context) error {
	// Determine the starting timestamp from the last ingested trade.
	lastTimestamp, err := o.tradeProcessor.tradeSvc.GetLastTimestamp(ctx)
	if err != nil {
		o.logger.Warn("could not get last trade timestamp, starting from 24h ago",
			slog.String("error", err.Error()),
		)
		lastTimestamp = time.Now().UTC().Add(-24 * time.Hour)
	}

	// Run immediately on start.
	fills, err := o.goldskyScraper.Run(ctx, lastTimestamp)
	if err != nil {
		o.logger.Error("goldsky scrape failed", slog.String("error", err.Error()))
	} else if len(fills) > 0 {
		processed, procErr := o.tradeProcessor.ProcessFills(ctx, fills)
		if procErr != nil {
			o.logger.Error("trade processing failed", slog.String("error", procErr.Error()))
		} else {
			o.logger.Info("processed fills from goldsky", slog.Int("count", processed))
		}
		lastTimestamp = latestFillTimestamp(fills, lastTimestamp)
	}

	ticker := time.NewTicker(o.scrapeInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.logger.Info("goldsky + trade processor loop stopped")
			return ctx.Err()
		case <-ticker.C:
			fills, err := o.goldskyScraper.Run(ctx, lastTimestamp)
			if err != nil {
				o.logger.Error("goldsky scrape failed", slog.String("error", err.Error()))
				continue
			}

			if len(fills) == 0 {
				continue
			}

			processed, procErr := o.tradeProcessor.ProcessFills(ctx, fills)
			if procErr != nil {
				o.logger.Error("trade processing failed", slog.String("error", procErr.Error()))
				continue
			}

			o.logger.Info("processed fills from goldsky", slog.Int("count", processed))
			lastTimestamp = latestFillTimestamp(fills, lastTimestamp)
		}
	}
}
