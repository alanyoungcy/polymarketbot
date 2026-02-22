// Package arbitrage provides selectable single-venue (Polymarket) arbitrage
// strategies and a detector that runs the chosen strategy on orderbook data.
package arbitrage

import (
	"context"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// Strategy is a single-venue arbitrage strategy that detects opportunities
// from orderbook snapshots.
type Strategy interface {
	Name() string
	// Detect returns zero or more opportunities for the given snapshot.
	// Single-venue: KalshiMarketID and KalshiPrice are left empty.
	Detect(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.ArbOpportunity, error)
}
