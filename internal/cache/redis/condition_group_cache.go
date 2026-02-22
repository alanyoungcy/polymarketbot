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

const cgTTL = 5 * time.Minute

// ConditionGroupCache implements domain.ConditionGroupCache using Redis hashes
// with JSON-serialized ConditionGroup data and a secondary market-to-group index.
//
// Key schema:
//
//	cg:{id}          - hash with field "data" containing JSON
//	cg:mkt:{marketID} - string value of the group ID
type ConditionGroupCache struct {
	rdb *redis.Client
}

// NewConditionGroupCache creates a ConditionGroupCache backed by the given Client.
func NewConditionGroupCache(c *Client) *ConditionGroupCache {
	return &ConditionGroupCache{rdb: c.Underlying()}
}

func cgKey(id string) string        { return "cg:" + id }
func cgMarketKey(mid string) string { return "cg:mkt:" + mid }

// Set stores a ConditionGroup in the cache with a 5-minute TTL.
func (c *ConditionGroupCache) Set(ctx context.Context, group domain.ConditionGroup) error {
	data, err := json.Marshal(group)
	if err != nil {
		return fmt.Errorf("redis: marshal condition group %s: %w", group.ID, err)
	}

	key := cgKey(group.ID)

	pipe := c.rdb.TxPipeline()
	pipe.HSet(ctx, key, "data", data)
	pipe.Expire(ctx, key, cgTTL)

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis: set condition group %s: %w", group.ID, err)
	}
	return nil
}

// Get retrieves a ConditionGroup by its ID from the cache.
// It returns domain.ErrNotFound when the key does not exist.
func (c *ConditionGroupCache) Get(ctx context.Context, id string) (domain.ConditionGroup, error) {
	data, err := c.rdb.HGet(ctx, cgKey(id), "data").Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.ConditionGroup{}, domain.ErrNotFound
		}
		return domain.ConditionGroup{}, fmt.Errorf("redis: get condition group %s: %w", id, err)
	}

	var group domain.ConditionGroup
	if err := json.Unmarshal(data, &group); err != nil {
		return domain.ConditionGroup{}, fmt.Errorf("redis: unmarshal condition group %s: %w", id, err)
	}
	return group, nil
}

// GetByMarketID looks up a ConditionGroup by one of its linked market IDs.
// It returns domain.ErrNotFound if the mapping or group does not exist.
func (c *ConditionGroupCache) GetByMarketID(ctx context.Context, marketID string) (domain.ConditionGroup, error) {
	groupID, err := c.rdb.Get(ctx, cgMarketKey(marketID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.ConditionGroup{}, domain.ErrNotFound
		}
		return domain.ConditionGroup{}, fmt.Errorf("redis: get condition group by market %s: %w", marketID, err)
	}

	return c.Get(ctx, groupID)
}

// Invalidate removes a ConditionGroup from the cache.
func (c *ConditionGroupCache) Invalidate(ctx context.Context, id string) error {
	if err := c.rdb.Del(ctx, cgKey(id)).Err(); err != nil {
		return fmt.Errorf("redis: invalidate condition group %s: %w", id, err)
	}
	return nil
}

// Compile-time interface check.
var _ domain.ConditionGroupCache = (*ConditionGroupCache)(nil)
