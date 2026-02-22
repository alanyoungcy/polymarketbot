package pipeline

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// FillFetcher retrieves raw on-chain order-filled events.
type FillFetcher interface {
	FetchOrderFills(ctx context.Context, since time.Time, first int) ([]domain.RawFill, error)
}

// GoldskyScraper scrapes on-chain trade events from Goldsky GraphQL, converts
// them to CSV, and uploads the result to object storage.
type GoldskyScraper struct {
	fetcher FillFetcher
	writer  domain.BlobWriter
	logger  *slog.Logger
}

// NewGoldskyScraper creates a new GoldskyScraper.
func NewGoldskyScraper(fetcher FillFetcher, writer domain.BlobWriter, logger *slog.Logger) *GoldskyScraper {
	return &GoldskyScraper{
		fetcher: fetcher,
		writer:  writer,
		logger:  logger,
	}
}

// Run executes a single scrape run. It fetches fills since the given timestamp,
// converts them to CSV, uploads the CSV to S3, and returns the fills for further
// processing.
func (s *GoldskyScraper) Run(ctx context.Context, since time.Time) ([]domain.RawFill, error) {
	const fetchLimit = 1000

	fills, err := s.fetcher.FetchOrderFills(ctx, since, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("fetching order fills since %v: %w", since, err)
	}

	if len(fills) == 0 {
		s.logger.Info("no new fills found", slog.Time("since", since))
		return fills, nil
	}

	csvData, err := fillsToCSV(fills)
	if err != nil {
		return nil, fmt.Errorf("converting fills to CSV: %w", err)
	}

	path := fmt.Sprintf("goldsky/orderFilled/%s.csv", time.Now().UTC().Format("2006-01-02"))
	if err := s.writer.Put(ctx, path, bytes.NewReader(csvData), "text/csv"); err != nil {
		return nil, fmt.Errorf("uploading CSV to %s: %w", path, err)
	}

	s.logger.Info("goldsky scrape complete",
		slog.Int("fills_count", len(fills)),
		slog.String("s3_path", path),
	)

	return fills, nil
}

// RunLoop runs the Goldsky scraper on a repeating interval until the context is
// cancelled. It tracks the last processed timestamp so each iteration only
// fetches new fills.
func (s *GoldskyScraper) RunLoop(ctx context.Context, interval time.Duration, lastTimestamp time.Time) error {
	// Run immediately on start.
	fills, err := s.Run(ctx, lastTimestamp)
	if err != nil {
		s.logger.Error("goldsky scrape failed", slog.String("error", err.Error()))
	} else if len(fills) > 0 {
		lastTimestamp = latestFillTimestamp(fills, lastTimestamp)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("goldsky scraper loop stopped")
			return ctx.Err()
		case <-ticker.C:
			fills, err := s.Run(ctx, lastTimestamp)
			if err != nil {
				s.logger.Error("goldsky scrape failed", slog.String("error", err.Error()))
				continue
			}
			if len(fills) > 0 {
				lastTimestamp = latestFillTimestamp(fills, lastTimestamp)
			}
		}
	}
}

// fillsToCSV converts a slice of RawFill to CSV bytes with a header row.
func fillsToCSV(fills []domain.RawFill) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	// Write header.
	header := []string{
		"timestamp",
		"maker",
		"maker_asset_id",
		"maker_amount_filled",
		"taker",
		"taker_asset_id",
		"taker_amount_filled",
		"transaction_hash",
	}
	if err := w.Write(header); err != nil {
		return nil, fmt.Errorf("writing CSV header: %w", err)
	}

	// Write rows.
	for _, f := range fills {
		row := []string{
			strconv.FormatInt(f.Timestamp, 10),
			f.Maker,
			f.MakerAssetID,
			strconv.FormatInt(f.MakerAmountFilled, 10),
			f.Taker,
			f.TakerAssetID,
			strconv.FormatInt(f.TakerAmountFilled, 10),
			f.TransactionHash,
		}
		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("writing CSV row: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flushing CSV writer: %w", err)
	}

	return buf.Bytes(), nil
}

// latestFillTimestamp returns the most recent timestamp from a slice of fills,
// or the fallback if the slice is empty.
func latestFillTimestamp(fills []domain.RawFill, fallback time.Time) time.Time {
	latest := fallback
	for _, f := range fills {
		ts := time.Unix(f.Timestamp, 0)
		if ts.After(latest) {
			latest = ts
		}
	}
	return latest
}
