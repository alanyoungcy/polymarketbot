package feed

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/strategy"
)

// priceEvent is the JSON shape published to "prices" (e.g. by PriceService).
type priceEvent struct {
	Event     string  `json:"event"`
	AssetID   string  `json:"asset_id"`
	BestBid   float64 `json:"best_bid"`
	BestAsk   float64 `json:"best_ask"`
	MidPrice  float64 `json:"mid_price"`
	Side      string  `json:"side"`
	Price     float64 `json:"price"`
	Size      float64 `json:"size"`
	Timestamp string  `json:"timestamp"`
}

// EngineFeeder subscribes to the "prices" Redis channel and feeds orderbook
// and price-change events into the strategy engine.
type EngineFeeder struct {
	bus       domain.SignalBus
	bookCache domain.OrderbookCache
	engine    *strategy.Engine
	logger    *slog.Logger
}

// NewEngineFeeder creates an EngineFeeder.
func NewEngineFeeder(bus domain.SignalBus, bookCache domain.OrderbookCache, engine *strategy.Engine, logger *slog.Logger) *EngineFeeder {
	return &EngineFeeder{
		bus:       bus,
		bookCache: bookCache,
		engine:    engine,
		logger:    logger.With(slog.String("component", "engine_feeder")),
	}
}

// Run subscribes to "prices" and calls engine.HandleBookUpdate or HandlePriceChange for each message.
func (f *EngineFeeder) Run(ctx context.Context) error {
	ch, err := f.bus.Subscribe(ctx, "prices")
	if err != nil {
		return err
	}
	f.logger.Info("engine feeder started")
	defer f.logger.Info("engine feeder stopped")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case data, ok := <-ch:
			if !ok {
				return nil
			}
			if err := f.handleMessage(ctx, data); err != nil {
				f.logger.Debug("engine feeder handle message failed",
					slog.String("error", err.Error()),
					slog.Int("payload_len", len(data)),
				)
			}
		}
	}
}

func (f *EngineFeeder) handleMessage(ctx context.Context, data []byte) error {
	var ev priceEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return err
	}
	assetID := strings.TrimSpace(ev.AssetID)
	if assetID == "" {
		return nil
	}
	ts := time.Now()
	if ev.Timestamp != "" {
		if t, err := time.Parse(time.RFC3339Nano, ev.Timestamp); err == nil {
			ts = t
		}
	}

	switch ev.Event {
	case "price_change":
		change := domain.PriceChange{
			AssetID:   assetID,
			Side:      ev.Side,
			Price:     ev.Price,
			Size:      ev.Size,
			Timestamp: ts,
		}
		return f.engine.HandlePriceChange(ctx, change)
	case "book_update":
		fallthrough
	default:
		snap, err := f.bookCache.GetSnapshot(ctx, assetID)
		if err != nil || snap.AssetID == "" {
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
		return f.engine.HandleBookUpdate(ctx, snap)
	}
}
