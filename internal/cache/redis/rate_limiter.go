package redis

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/redis/go-redis/v9"
)

//go:embed scripts/sliding_window.lua
var slidingWindowLua string

// defaultRateLimitWindow is used by Wait when the caller does not pass
// explicit limit/window params. Wait uses a fixed polling interval.
const waitPollInterval = 50 * time.Millisecond

// RateLimiter implements domain.RateLimiter using a sliding-window approach
// backed by Redis sorted sets and an atomic Lua script.
type RateLimiter struct {
	rdb           *redis.Client
	slidingWindow *redis.Script
}

// NewRateLimiter creates a RateLimiter backed by the given Client.
func NewRateLimiter(c *Client) *RateLimiter {
	return &RateLimiter{
		rdb:           c.Underlying(),
		slidingWindow: redis.NewScript(slidingWindowLua),
	}
}

func rateLimitKey(key string) string {
	return "ratelimit:" + key
}

// Allow checks whether a request for the given key is permitted under the
// sliding window rate limit. It returns true if the request is allowed (and
// the request is counted), or false if the limit has been reached.
func (rl *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	now := time.Now().UnixMicro()
	windowMicro := window.Microseconds()

	result, err := rl.slidingWindow.Run(
		ctx,
		rl.rdb,
		[]string{rateLimitKey(key)},
		now,
		windowMicro,
		limit,
	).Int64Slice()
	if err != nil {
		return false, fmt.Errorf("redis: rate limit allow %s: %w", key, err)
	}

	if len(result) < 2 {
		return false, fmt.Errorf("redis: rate limit allow %s: unexpected result length %d", key, len(result))
	}

	return result[0] == 1, nil
}

// Wait blocks until a request for the given key is allowed. It polls at a
// fixed interval, returning an error if the context is cancelled.
//
// Wait uses a default limit of 1 request per second. Callers that need custom
// limits should call Allow in their own loop.
func (rl *RateLimiter) Wait(ctx context.Context, key string) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("redis: rate limit wait %s: %w", key, ctx.Err())
		default:
		}

		allowed, err := rl.Allow(ctx, key, 1, time.Second)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}

		// Sleep before retrying, but honour the context.
		timer := time.NewTimer(waitPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("redis: rate limit wait %s: %w", key, ctx.Err())
		case <-timer.C:
		}
	}
}

// Compile-time interface check.
var _ domain.RateLimiter = (*RateLimiter)(nil)
