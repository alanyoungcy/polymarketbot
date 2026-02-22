package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// unlockLua is a Lua script that deletes a lock key only if its value matches
// the caller's unique token. This prevents one holder from accidentally
// releasing another holder's lock.
const unlockLua = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
    return redis.call('DEL', KEYS[1])
end
return 0
`

// LockManager implements domain.LockManager using Redis SETNX with a TTL and
// a Lua-based conditional unlock.
type LockManager struct {
	rdb      *redis.Client
	unlockSc *redis.Script
}

// NewLockManager creates a LockManager backed by the given Client.
func NewLockManager(c *Client) *LockManager {
	return &LockManager{
		rdb:      c.Underlying(),
		unlockSc: redis.NewScript(unlockLua),
	}
}

func lockKey(key string) string {
	return "lock:" + key
}

// Acquire attempts to obtain a distributed lock for the given key with the
// specified TTL. On success it returns an unlock function that must be called
// to release the lock. The unlock function is safe to call multiple times.
//
// It returns domain.ErrLockHeld if the lock is already held by another party.
func (lm *LockManager) Acquire(ctx context.Context, key string, ttl time.Duration) (func(), error) {
	token := uuid.New().String()
	lk := lockKey(key)

	ok, err := lm.rdb.SetNX(ctx, lk, token, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: acquire lock %s: %w", key, err)
	}
	if !ok {
		return nil, domain.ErrLockHeld
	}

	// Build the unlock closure. It is safe to call more than once.
	released := false
	unlock := func() {
		if released {
			return
		}
		released = true

		// Use a background context so unlock succeeds even if the caller's
		// context is already cancelled.
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = lm.unlockSc.Run(unlockCtx, lm.rdb, []string{lk}, token).Err()
	}

	return unlock, nil
}

// Compile-time interface check.
var _ domain.LockManager = (*LockManager)(nil)
