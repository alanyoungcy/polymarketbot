package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/redis/go-redis/v9"
)

// PriceCache implements domain.PriceCache using Redis hashes.
// Each asset's price is stored as a hash at key "price:{assetID}" with fields
// "price" and "ts" (Unix nanosecond timestamp).
type PriceCache struct {
	rdb *redis.Client
}

// NewPriceCache creates a PriceCache backed by the given Client.
func NewPriceCache(c *Client) *PriceCache {
	return &PriceCache{rdb: c.Underlying()}
}

func priceKey(assetID string) string {
	return "price:" + assetID
}

// SetPrice stores the latest price and timestamp for an asset.
func (pc *PriceCache) SetPrice(ctx context.Context, assetID string, price float64, ts time.Time) error {
	key := priceKey(assetID)
	fields := map[string]interface{}{
		"price": strconv.FormatFloat(price, 'f', -1, 64),
		"ts":    strconv.FormatInt(ts.UnixNano(), 10),
	}
	if err := pc.rdb.HSet(ctx, key, fields).Err(); err != nil {
		return fmt.Errorf("redis: set price %s: %w", assetID, err)
	}
	return nil
}

// GetPrice retrieves the latest price and timestamp for an asset.
// It returns domain.ErrNotFound when the key does not exist.
func (pc *PriceCache) GetPrice(ctx context.Context, assetID string) (float64, time.Time, error) {
	key := priceKey(assetID)
	vals, err := pc.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("redis: get price %s: %w", assetID, err)
	}
	if len(vals) == 0 {
		return 0, time.Time{}, domain.ErrNotFound
	}

	priceStr, ok := vals["price"]
	if !ok {
		return 0, time.Time{}, domain.ErrNotFound
	}
	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("redis: parse price %s: %w", assetID, err)
	}

	tsStr, ok := vals["ts"]
	if !ok {
		return 0, time.Time{}, domain.ErrNotFound
	}
	tsNano, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("redis: parse ts %s: %w", assetID, err)
	}

	return price, time.Unix(0, tsNano), nil
}

// GetPrices retrieves the latest prices for multiple assets using a pipeline.
// Assets whose keys do not exist are silently omitted from the result map.
func (pc *PriceCache) GetPrices(ctx context.Context, assetIDs []string) (map[string]float64, error) {
	if len(assetIDs) == 0 {
		return map[string]float64{}, nil
	}

	pipe := pc.rdb.Pipeline()
	cmds := make(map[string]*redis.MapStringStringCmd, len(assetIDs))
	for _, id := range assetIDs {
		cmds[id] = pipe.HGetAll(ctx, priceKey(id))
	}

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("redis: get prices pipeline: %w", err)
	}

	result := make(map[string]float64, len(assetIDs))
	for id, cmd := range cmds {
		vals, err := cmd.Result()
		if err != nil {
			continue
		}
		if len(vals) == 0 {
			continue
		}
		priceStr, ok := vals["price"]
		if !ok {
			continue
		}
		price, err := strconv.ParseFloat(priceStr, 64)
		if err != nil {
			continue
		}
		result[id] = price
	}

	return result, nil
}

// Compile-time interface check.
var _ domain.PriceCache = (*PriceCache)(nil)
