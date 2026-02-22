package strategy

import (
	"math"
	"sync"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// PricePoint records a single price observation at a point in time.
type PricePoint struct {
	Price float64
	Time  time.Time
}

// PriceTracker maintains a sliding window of recent prices for each asset and
// exposes statistical helpers that strategies rely on.
type PriceTracker struct {
	prices     domain.PriceCache
	history    map[string][]PricePoint
	windowSize time.Duration
	mu         sync.RWMutex
}

// NewPriceTracker creates a PriceTracker backed by the given PriceCache. The
// windowSize parameter controls how far back the in-memory history extends;
// points older than the window are discarded on every Track call.
func NewPriceTracker(prices domain.PriceCache, windowSize time.Duration) *PriceTracker {
	return &PriceTracker{
		prices:     prices,
		history:    make(map[string][]PricePoint),
		windowSize: windowSize,
	}
}

// Track records a new price observation for the given asset and trims points
// that have fallen outside the sliding window.
func (pt *PriceTracker) Track(assetID string, price float64, ts time.Time) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.history[assetID] = append(pt.history[assetID], PricePoint{
		Price: price,
		Time:  ts,
	})
	pt.trim(assetID, ts)
}

// GetHistory returns a copy of the price history within the sliding window for
// the given asset. The returned slice is safe to mutate.
func (pt *PriceTracker) GetHistory(assetID string) []PricePoint {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	src := pt.history[assetID]
	if len(src) == 0 {
		return nil
	}
	out := make([]PricePoint, len(src))
	copy(out, src)
	return out
}

// GetAverage returns the arithmetic mean of all prices in the sliding window.
// If there are no recorded points, it returns 0.
func (pt *PriceTracker) GetAverage(assetID string) float64 {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	pts := pt.history[assetID]
	if len(pts) == 0 {
		return 0
	}
	var sum float64
	for _, p := range pts {
		sum += p.Price
	}
	return sum / float64(len(pts))
}

// GetVolatility returns the population standard deviation of the prices in the
// sliding window. If there are fewer than two points, it returns 0.
func (pt *PriceTracker) GetVolatility(assetID string) float64 {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	pts := pt.history[assetID]
	if len(pts) < 2 {
		return 0
	}

	var sum float64
	for _, p := range pts {
		sum += p.Price
	}
	mean := sum / float64(len(pts))

	var variance float64
	for _, p := range pts {
		d := p.Price - mean
		variance += d * d
	}
	variance /= float64(len(pts))
	return math.Sqrt(variance)
}

// DetectFlashCrash returns true when the most recent price has dropped by more
// than threshold (as a fraction, e.g. 0.10 for 10 %) relative to the recent
// average.
func (pt *PriceTracker) DetectFlashCrash(assetID string, threshold float64) bool {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	pts := pt.history[assetID]
	if len(pts) < 2 {
		return false
	}

	// Compute the average of all points except the last one so the current
	// price does not drag the average down.
	var sum float64
	n := len(pts) - 1
	for i := 0; i < n; i++ {
		sum += pts[i].Price
	}
	avg := sum / float64(n)
	if avg == 0 {
		return false
	}

	current := pts[len(pts)-1].Price
	drop := (avg - current) / avg
	return drop >= threshold
}

// trim removes all points older than windowSize relative to the reference time.
// The caller must hold pt.mu.
func (pt *PriceTracker) trim(assetID string, now time.Time) {
	cutoff := now.Add(-pt.windowSize)
	pts := pt.history[assetID]

	// Find the first index that is within the window.
	i := 0
	for i < len(pts) && pts[i].Time.Before(cutoff) {
		i++
	}
	if i > 0 {
		pt.history[assetID] = pts[i:]
	}
}
