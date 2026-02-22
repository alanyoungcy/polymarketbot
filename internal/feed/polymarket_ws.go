package feed

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/platform/polymarket"
)

// BookUpdateHandler is called for each orderbook snapshot (PriceService + Engine).
type BookUpdateHandler func(ctx context.Context, snap domain.OrderbookSnapshot)

// PriceChangeHandler is called for each price change (PriceService + Engine).
type PriceChangeHandler func(ctx context.Context, change domain.PriceChange)

// PolymarketWSFeed connects to the Polymarket CLOB WebSocket, subscribes to
// book and price_change for the given asset IDs, and invokes the provided
// handlers on each message. It reconnects on disconnect.
type PolymarketWSFeed struct {
	wsURL     string
	assetIDs  []string
	onBook    BookUpdateHandler
	onPrice   PriceChangeHandler
	logger    *slog.Logger
	closeOnce sync.Once
	done      chan struct{}
}

// NewPolymarketWSFeed creates a feed that will subscribe to the given asset IDs.
func NewPolymarketWSFeed(wsURL string, assetIDs []string, onBook BookUpdateHandler, onPrice PriceChangeHandler, logger *slog.Logger) *PolymarketWSFeed {
	return &PolymarketWSFeed{
		wsURL:    wsURL,
		assetIDs: assetIDs,
		onBook:   onBook,
		onPrice:  onPrice,
		logger:   logger.With(slog.String("component", "polymarket_ws_feed")),
		done:     make(chan struct{}),
	}
}

// Run connects, subscribes to book and price_change for the configured assets,
// and runs until ctx is cancelled. Reconnects with backoff on disconnect.
func (f *PolymarketWSFeed) Run(ctx context.Context) error {
	if len(f.assetIDs) == 0 {
		f.logger.Info("no asset IDs to subscribe, exiting")
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-f.done:
			return nil
		default:
		}
		connCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := f.runConnection(connCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f.logger.Warn("polymarket ws disconnected, reconnecting", slog.String("error", err.Error()))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func (f *PolymarketWSFeed) runConnection(ctx context.Context) error {
	client := polymarket.NewWSClient(f.wsURL)
	defer client.Close()

	client.OnBookUpdate(func(snap domain.OrderbookSnapshot) {
		if f.onBook != nil {
			f.onBook(context.Background(), snap)
		}
	})
	client.OnPriceChange(func(change domain.PriceChange) {
		if f.onPrice != nil {
			f.onPrice(context.Background(), change)
		}
	})

	if err := client.Connect(ctx); err != nil {
		return err
	}
	channels := []string{"book", "price_change"}
	if err := client.Subscribe(ctx, channels, f.assetIDs); err != nil {
		return err
	}
	f.logger.Info("polymarket ws subscribed", slog.Int("assets", len(f.assetIDs)))

	<-ctx.Done()
	return ctx.Err()
}

// Close stops the feed.
func (f *PolymarketWSFeed) Close() {
	f.closeOnce.Do(func() { close(f.done) })
}
