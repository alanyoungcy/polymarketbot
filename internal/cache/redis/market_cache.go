package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/redis/go-redis/v9"
)

const marketTTL = 5 * time.Minute

// MarketCache implements domain.MarketCache using Redis hashes with JSON-
// serialized Market data and a secondary token-to-market index.
//
// Key schema:
//
//	market:{id}            - hash with field "data" containing JSON
//	market:token:{tokenID} - string value of the market ID
type MarketCache struct {
	rdb *redis.Client
}

// NewMarketCache creates a MarketCache backed by the given Client.
func NewMarketCache(c *Client) *MarketCache {
	return &MarketCache{rdb: c.Underlying()}
}

func marketKey(id string) string       { return "market:" + id }
func marketTokenKey(tok string) string { return "market:token:" + tok }

// Set stores a Market in the cache with a 5-minute TTL. It also creates
// token-to-market index entries for both of the market's token IDs.
func (mc *MarketCache) Set(ctx context.Context, market domain.Market) error {
	data, err := json.Marshal(market)
	if err != nil {
		return fmt.Errorf("redis: marshal market %s: %w", market.ID, err)
	}

	key := marketKey(market.ID)

	pipe := mc.rdb.TxPipeline()
	pipe.HSet(ctx, key, "data", data)
	pipe.Expire(ctx, key, marketTTL)

	// Index both token IDs to this market.
	for _, tokenID := range market.TokenIDs {
		if tokenID == "" {
			continue
		}
		tokKey := marketTokenKey(tokenID)
		pipe.Set(ctx, tokKey, market.ID, marketTTL)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: set market %s: %w", market.ID, err)
	}
	return nil
}

// Get retrieves a Market by its ID from the cache.
// It returns domain.ErrNotFound when the key does not exist.
func (mc *MarketCache) Get(ctx context.Context, id string) (domain.Market, error) {
	data, err := mc.rdb.HGet(ctx, marketKey(id), "data").Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.Market{}, domain.ErrNotFound
		}
		return domain.Market{}, fmt.Errorf("redis: get market %s: %w", id, err)
	}

	var market domain.Market
	if err := json.Unmarshal(data, &market); err != nil {
		return domain.Market{}, fmt.Errorf("redis: unmarshal market %s: %w", id, err)
	}
	return market, nil
}

// GetByToken looks up a Market by one of its ERC-1155 token IDs.
// It returns domain.ErrNotFound if the token mapping or market does not exist.
func (mc *MarketCache) GetByToken(ctx context.Context, tokenID string) (domain.Market, error) {
	marketID, err := mc.rdb.Get(ctx, marketTokenKey(tokenID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.Market{}, domain.ErrNotFound
		}
		return domain.Market{}, fmt.Errorf("redis: get market by token %s: %w", tokenID, err)
	}

	return mc.Get(ctx, marketID)
}

// Invalidate removes a Market and its token index entries from the cache.
func (mc *MarketCache) Invalidate(ctx context.Context, id string) error {
	// First, retrieve the market to find its token IDs so we can clean up
	// the reverse index entries.
	market, err := mc.Get(ctx, id)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("redis: invalidate market %s: %w", id, err)
	}

	pipe := mc.rdb.TxPipeline()
	pipe.Del(ctx, marketKey(id))

	// Only delete token mappings if we successfully read the market.
	if err == nil {
		for _, tokenID := range market.TokenIDs {
			if tokenID == "" {
				continue
			}
			pipe.Del(ctx, marketTokenKey(tokenID))
		}
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: invalidate market %s: %w", id, err)
	}
	return nil
}

// Compile-time interface check.
var _ domain.MarketCache = (*MarketCache)(nil)
