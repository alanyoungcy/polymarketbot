package executor

import (
	"sync"
	"time"
)

// Dedup prevents duplicate trade signals from being executed more than once
// within a configurable time-to-live window. It is safe for concurrent use.
type Dedup struct {
	seen map[string]time.Time // signalID -> last seen time
	ttl  time.Duration
	mu   sync.Mutex
}

// NewDedup creates a Dedup instance that considers a signal a duplicate if it
// has been seen within the given ttl.
func NewDedup(ttl time.Duration) *Dedup {
	return &Dedup{
		seen: make(map[string]time.Time),
		ttl:  ttl,
	}
}

// IsDuplicate returns true if the signalID has been seen within the TTL
// window. If the signal has not been seen (or has expired), it is recorded and
// false is returned.
func (d *Dedup) IsDuplicate(signalID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	if lastSeen, ok := d.seen[signalID]; ok {
		if now.Sub(lastSeen) < d.ttl {
			return true
		}
	}

	d.seen[signalID] = now
	return false
}

// Cleanup removes entries that have expired beyond the TTL. This should be
// called periodically to prevent unbounded memory growth.
func (d *Dedup) Cleanup() {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for id, ts := range d.seen {
		if now.Sub(ts) >= d.ttl {
			delete(d.seen, id)
		}
	}
}
