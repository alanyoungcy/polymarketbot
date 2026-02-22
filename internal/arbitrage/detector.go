package arbitrage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/service"
)

// Detector runs the selected arbitrage strategy on orderbook updates from the
// "prices" channel and evaluates/records opportunities via ArbService.
type Detector struct {
	strategy Strategy
	arbSvc   *service.ArbService
	bookCache domain.OrderbookCache
	logger   *slog.Logger
}

// DetectorConfig configures the detector.
type DetectorConfig struct {
	Strategy Strategy
	ArbSvc   *service.ArbService
	BookCache domain.OrderbookCache
	Logger   *slog.Logger
}

// NewDetector creates a detector that runs the given strategy.
func NewDetector(cfg DetectorConfig) *Detector {
	return &Detector{
		strategy:  cfg.Strategy,
		arbSvc:   cfg.ArbSvc,
		bookCache: cfg.BookCache,
		logger:    cfg.Logger.With(slog.String("component", "arb_detector")),
	}
}

// priceEvent is the JSON shape published by PriceService to "prices".
type priceEvent struct {
	Event     string  `json:"event"`
	AssetID   string  `json:"asset_id"`
	BestBid   float64 `json:"best_bid"`
	BestAsk   float64 `json:"best_ask"`
	MidPrice  float64 `json:"mid_price"`
	Timestamp string  `json:"timestamp"`
}

// Run subscribes to the "prices" channel and runs the strategy on each update.
// It blocks until ctx is cancelled.
func (d *Detector) Run(ctx context.Context, bus domain.SignalBus) error {
	ch, err := bus.Subscribe(ctx, "prices")
	if err != nil {
		return fmt.Errorf("arb detector: subscribe prices: %w", err)
	}
	d.logger.Info("arb detector started", slog.String("strategy", d.strategy.Name()))
	defer d.logger.Info("arb detector stopped")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data, ok := <-ch:
			if !ok {
				return nil
			}
			if err := d.handleMessage(ctx, data); err != nil {
				d.logger.Warn("arb detector: handle message failed",
					slog.String("error", err.Error()),
					slog.String("payload", string(data)),
				)
			}
		}
	}
}

func (d *Detector) handleMessage(ctx context.Context, data []byte) error {
	var ev priceEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}
	if ev.Event != "book_update" && ev.Event != "price_change" {
		return nil
	}
	assetID := strings.TrimSpace(ev.AssetID)
	if assetID == "" {
		return nil
	}
	// Get full snapshot from cache so strategies have Bids/Asks (e.g. imbalance).
	snap, err := d.bookCache.GetSnapshot(ctx, assetID)
	if err != nil || snap.AssetID == "" {
		// Build minimal snapshot from event if cache miss (e.g. first message).
		ts := time.Now()
		if ev.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
				ts = t
			}
		}
		snap = domain.OrderbookSnapshot{
			AssetID:   assetID,
			BestBid:   ev.BestBid,
			BestAsk:   ev.BestAsk,
			MidPrice:  ev.MidPrice,
			Timestamp: ts,
		}
		if ev.BestBid > 0 {
			snap.Bids = []domain.PriceLevel{{Price: ev.BestBid, Size: 0}}
		}
		if ev.BestAsk > 0 {
			snap.Asks = []domain.PriceLevel{{Price: ev.BestAsk, Size: 0}}
		}
	}

	opps, err := d.strategy.Detect(ctx, snap)
	if err != nil {
		return fmt.Errorf("strategy detect: %w", err)
	}
	for _, opp := range opps {
		ok, err := d.arbSvc.Evaluate(ctx, opp)
		if err != nil {
			d.logger.Warn("arb evaluate failed", slog.String("opp_id", opp.ID), slog.String("error", err.Error()))
			continue
		}
		if !ok {
			continue
		}
		if err := d.arbSvc.Record(ctx, opp); err != nil {
			d.logger.Warn("arb record failed", slog.String("opp_id", opp.ID), slog.String("error", err.Error()))
		}
	}
	return nil
}
