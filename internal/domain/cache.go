package domain

import (
	"context"
	"time"
)

// PriceCache provides fast access to the latest prices.
type PriceCache interface {
	SetPrice(ctx context.Context, assetID string, price float64, ts time.Time) error
	GetPrice(ctx context.Context, assetID string) (float64, time.Time, error)
	GetPrices(ctx context.Context, assetIDs []string) (map[string]float64, error)
}

// OrderbookCache stores live orderbook state.
type OrderbookCache interface {
	SetSnapshot(ctx context.Context, assetID string, snap OrderbookSnapshot) error
	GetSnapshot(ctx context.Context, assetID string) (OrderbookSnapshot, error)
	UpdateLevel(ctx context.Context, assetID string, side string, price, size float64) error
	GetBBO(ctx context.Context, assetID string) (bestBid, bestAsk float64, err error)
}

// MarketCache provides fast market metadata lookups.
type MarketCache interface {
	Set(ctx context.Context, market Market) error
	Get(ctx context.Context, id string) (Market, error)
	GetByToken(ctx context.Context, tokenID string) (Market, error)
	Invalidate(ctx context.Context, id string) error
}

// ConditionGroupCache provides fast condition group lookups.
type ConditionGroupCache interface {
	Set(ctx context.Context, group ConditionGroup) error
	Get(ctx context.Context, id string) (ConditionGroup, error)
	GetByMarketID(ctx context.Context, marketID string) (ConditionGroup, error)
	Invalidate(ctx context.Context, id string) error
}

// RateLimiter provides distributed rate limiting.
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
	Wait(ctx context.Context, key string) error
}

// LockManager provides distributed locking.
type LockManager interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (unlock func(), err error)
}

// StreamMessage represents a single entry from a Redis stream.
type StreamMessage struct {
	ID      string
	Payload []byte
}

// SignalBus provides pub/sub and durable streams.
type SignalBus interface {
	Publish(ctx context.Context, channel string, payload []byte) error
	Subscribe(ctx context.Context, channel string) (<-chan []byte, error)
	StreamAppend(ctx context.Context, stream string, payload []byte) error
	StreamRead(ctx context.Context, stream string, lastID string, count int) ([]StreamMessage, error)
}
