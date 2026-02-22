// yes_no_spread implements a strategy that looks at yes and no token orderbooks
// for the same market; if yes_bid + no_bid < 1 or yes_ask + no_ask > 1 there is
// a theoretical edge. This strategy requires market-level grouping (multiple
// tokens per market). For single-token snapshots we treat it as no opportunity.
package arbitrage

import (
	"context"
	"log/slog"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// YesNoSpreadConfig configures the yes/no spread strategy.
type YesNoSpreadConfig struct {
	MinEdgeBps       float64 // minimum edge in bps (e.g. 100 = 1%)
	EstFeeBps        float64
	EstSlippageBps   float64
	EstLatencyBps    float64
	MaxAmount        float64
}

// YesNoSpread detects opportunities when yes and no prices sum to non-1
// (requires both tokens' snapshots; single snapshot cannot detect). This
// implementation is a no-op for a single OrderbookSnapshot; a detector could
// aggregate by market and call a different API. We register it so "yes_no_spread"
// is selectable and return no opportunities from a single snap.
type YesNoSpread struct {
	cfg    YesNoSpreadConfig
	logger *slog.Logger
}

// NewYesNoSpread creates a yes/no spread strategy.
func NewYesNoSpread(cfg YesNoSpreadConfig, logger *slog.Logger) *YesNoSpread {
	return &YesNoSpread{cfg: cfg, logger: logger.With(slog.String("arb_strategy", "yes_no_spread"))}
}

// Name returns the strategy identifier.
func (y *YesNoSpread) Name() string { return "yes_no_spread" }

// Detect returns no opportunities for a single snapshot; yes/no arb requires
// paired token data (caller would need to group by market and pass combined state).
func (y *YesNoSpread) Detect(ctx context.Context, snap domain.OrderbookSnapshot) ([]domain.ArbOpportunity, error) {
	_ = ctx
	_ = snap
	// Single snapshot: cannot compute yes + no. Return empty; detector could
	// maintain market-level state and call a different method when both tokens available.
	return nil, nil
}
