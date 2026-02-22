package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// PriceService handles orderbook updates and price tracking by coordinating
// the price cache, orderbook cache, and signal bus.
type PriceService struct {
	priceCache domain.PriceCache
	bookCache  domain.OrderbookCache
	bus        domain.SignalBus
	logger     *slog.Logger
}

// NewPriceService creates a PriceService with all required dependencies.
func NewPriceService(
	priceCache domain.PriceCache,
	bookCache domain.OrderbookCache,
	bus domain.SignalBus,
	logger *slog.Logger,
) *PriceService {
	return &PriceService{
		priceCache: priceCache,
		bookCache:  bookCache,
		bus:        bus,
		logger:     logger,
	}
}

// HandleBookUpdate processes a full orderbook snapshot: persists it in the
// orderbook cache, updates the mid-price in the price cache, and publishes
// a price update event.
func (s *PriceService) HandleBookUpdate(ctx context.Context, snap domain.OrderbookSnapshot) error {
	// Store the full orderbook snapshot.
	if err := s.bookCache.SetSnapshot(ctx, snap.AssetID, snap); err != nil {
		return fmt.Errorf("price_service: set snapshot for %q: %w", snap.AssetID, err)
	}

	// Update the mid-price in the price cache.
	if err := s.priceCache.SetPrice(ctx, snap.AssetID, snap.MidPrice, snap.Timestamp); err != nil {
		return fmt.Errorf("price_service: set price for %q: %w", snap.AssetID, err)
	}

	// Publish price update event.
	evt, _ := json.Marshal(map[string]any{
		"event":     "book_update",
		"asset_id":  snap.AssetID,
		"best_bid":  snap.BestBid,
		"best_ask":  snap.BestAsk,
		"mid_price": snap.MidPrice,
		"timestamp": snap.Timestamp.Format(time.RFC3339Nano),
	})
	if pubErr := s.bus.Publish(ctx, "prices", evt); pubErr != nil {
		s.logger.WarnContext(ctx, "price_service: publish book update event failed",
			slog.String("asset_id", snap.AssetID),
			slog.String("error", pubErr.Error()),
		)
	}

	return nil
}

// HandlePriceChange processes an incremental orderbook level update:
// updates the specific level in the orderbook cache, recomputes the BBO,
// updates the mid-price, and publishes an event.
func (s *PriceService) HandlePriceChange(ctx context.Context, change domain.PriceChange) error {
	// Update the specific level in the orderbook.
	if err := s.bookCache.UpdateLevel(ctx, change.AssetID, change.Side, change.Price, change.Size); err != nil {
		return fmt.Errorf("price_service: update level for %q: %w", change.AssetID, err)
	}

	// Recompute BBO from the orderbook cache.
	bestBid, bestAsk, err := s.bookCache.GetBBO(ctx, change.AssetID)
	if err != nil {
		return fmt.Errorf("price_service: get BBO for %q: %w", change.AssetID, err)
	}

	// Compute and store the new mid-price.
	var midPrice float64
	if bestBid > 0 && bestAsk > 0 {
		midPrice = (bestBid + bestAsk) / 2
	}

	if err := s.priceCache.SetPrice(ctx, change.AssetID, midPrice, change.Timestamp); err != nil {
		return fmt.Errorf("price_service: set price for %q: %w", change.AssetID, err)
	}

	// Publish price change event.
	evt, _ := json.Marshal(map[string]any{
		"event":     "price_change",
		"asset_id":  change.AssetID,
		"side":      change.Side,
		"price":     change.Price,
		"size":      change.Size,
		"best_bid":  bestBid,
		"best_ask":  bestAsk,
		"mid_price": midPrice,
		"timestamp": change.Timestamp.Format(time.RFC3339Nano),
	})
	if pubErr := s.bus.Publish(ctx, "prices", evt); pubErr != nil {
		s.logger.WarnContext(ctx, "price_service: publish price change event failed",
			slog.String("asset_id", change.AssetID),
			slog.String("error", pubErr.Error()),
		)
	}

	return nil
}

// GetPrice returns the latest cached price and its timestamp for a single asset.
func (s *PriceService) GetPrice(ctx context.Context, assetID string) (float64, time.Time, error) {
	price, ts, err := s.priceCache.GetPrice(ctx, assetID)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("price_service: get price for %q: %w", assetID, err)
	}
	return price, ts, nil
}

// GetPrices returns the latest cached prices for multiple assets. Missing
// assets are omitted from the returned map.
func (s *PriceService) GetPrices(ctx context.Context, assetIDs []string) (map[string]float64, error) {
	prices, err := s.priceCache.GetPrices(ctx, assetIDs)
	if err != nil {
		return nil, fmt.Errorf("price_service: get prices: %w", err)
	}
	return prices, nil
}

// GetBBO returns the best bid and best ask for the given asset from the
// orderbook cache.
func (s *PriceService) GetBBO(ctx context.Context, assetID string) (float64, float64, error) {
	bestBid, bestAsk, err := s.bookCache.GetBBO(ctx, assetID)
	if err != nil {
		return 0, 0, fmt.Errorf("price_service: get BBO for %q: %w", assetID, err)
	}
	return bestBid, bestAsk, nil
}
