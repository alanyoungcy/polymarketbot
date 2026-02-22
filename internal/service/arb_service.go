package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ArbConfig holds the tunable parameters for the net-edge arbitrage model.
type ArbConfig struct {
	MinNetEdgeBps       float64
	MaxTradeAmount      float64
	MinDurationMs       int64
	MaxLegGapMs         int64
	MaxUnhedgedNotional float64
	KillSwitchLossUSD   float64
	PerVenueFeeBps      map[string]float64
}

// ArbService evaluates and records arbitrage opportunities using the
// net-edge model. All execution gates must pass before an opportunity
// is considered actionable.
type ArbService struct {
	arb    domain.ArbStore
	bus    domain.SignalBus
	audit  domain.AuditStore
	cfg    ArbConfig
	logger *slog.Logger
}

// NewArbService creates an ArbService with all required dependencies.
func NewArbService(
	arb domain.ArbStore,
	bus domain.SignalBus,
	audit domain.AuditStore,
	cfg ArbConfig,
	logger *slog.Logger,
) *ArbService {
	return &ArbService{
		arb:    arb,
		bus:    bus,
		audit:  audit,
		cfg:    cfg,
		logger: logger,
	}
}

// Evaluate applies the net-edge model to an arbitrage opportunity and
// returns true if all execution gates pass. The net edge is computed as:
//
//	net_edge_bps = gross_edge_bps - est_fee_bps - est_slippage_bps - est_latency_bps
//
// Execution gates (all must pass):
//  1. net_edge_bps >= MinNetEdgeBps
//  2. duration_ms >= MinDurationMs
//  3. unhedged exposure <= MaxUnhedgedNotional
//  4. session PnL drawdown >= -KillSwitchLossUSD (i.e. expected PnL is not below kill switch)
func (s *ArbService) Evaluate(ctx context.Context, opp domain.ArbOpportunity) (bool, error) {
	// Compute net edge.
	netEdgeBps := opp.GrossEdgeBps - opp.EstFeeBps - opp.EstSlippageBps - opp.EstLatencyBps

	// Gate 1: minimum net edge.
	if netEdgeBps < s.cfg.MinNetEdgeBps {
		s.logger.DebugContext(ctx, "arb_service: net edge below minimum",
			slog.String("opp_id", opp.ID),
			slog.Float64("net_edge_bps", netEdgeBps),
			slog.Float64("min_net_edge_bps", s.cfg.MinNetEdgeBps),
		)
		return false, nil
	}

	// Gate 2: minimum duration.
	durationMs := opp.Duration.Milliseconds()
	if durationMs < s.cfg.MinDurationMs {
		s.logger.DebugContext(ctx, "arb_service: duration below minimum",
			slog.String("opp_id", opp.ID),
			slog.Int64("duration_ms", durationMs),
			slog.Int64("min_duration_ms", s.cfg.MinDurationMs),
		)
		return false, nil
	}

	// Gate 3: unhedged exposure.
	if opp.MaxAmount > s.cfg.MaxUnhedgedNotional {
		s.logger.DebugContext(ctx, "arb_service: unhedged exposure exceeds limit",
			slog.String("opp_id", opp.ID),
			slog.Float64("max_amount", opp.MaxAmount),
			slog.Float64("max_unhedged", s.cfg.MaxUnhedgedNotional),
		)
		return false, nil
	}

	// Gate 4: kill switch - check if expected PnL is above the negative kill switch threshold.
	if opp.ExpectedPnLUSD < -s.cfg.KillSwitchLossUSD {
		s.logger.WarnContext(ctx, "arb_service: kill switch triggered",
			slog.String("opp_id", opp.ID),
			slog.Float64("expected_pnl", opp.ExpectedPnLUSD),
			slog.Float64("kill_switch", s.cfg.KillSwitchLossUSD),
		)
		return false, nil
	}

	s.logger.InfoContext(ctx, "arb_service: opportunity passed all gates",
		slog.String("opp_id", opp.ID),
		slog.Float64("net_edge_bps", netEdgeBps),
		slog.Int64("duration_ms", durationMs),
		slog.Float64("expected_pnl", opp.ExpectedPnLUSD),
	)

	return true, nil
}

// Record persists an arbitrage opportunity to the store and publishes it
// to the signal bus for downstream consumers.
func (s *ArbService) Record(ctx context.Context, opp domain.ArbOpportunity) error {
	if err := s.arb.Insert(ctx, opp); err != nil {
		return fmt.Errorf("arb_service: insert opportunity: %w", err)
	}

	// Publish to bus.
	evt, _ := json.Marshal(map[string]any{
		"event":          "arb_detected",
		"opp_id":         opp.ID,
		"poly_market":    opp.PolyMarketID,
		"kalshi_market":  opp.KalshiMarketID,
		"direction":      opp.Direction,
		"net_edge_bps":   opp.NetEdgeBps,
		"expected_pnl":   opp.ExpectedPnLUSD,
		"gross_edge_bps": opp.GrossEdgeBps,
	})
	if pubErr := s.bus.Publish(ctx, "arb", evt); pubErr != nil {
		s.logger.WarnContext(ctx, "arb_service: publish event failed",
			slog.String("opp_id", opp.ID),
			slog.String("error", pubErr.Error()),
		)
	}

	// Audit log.
	if auditErr := s.audit.Log(ctx, "arb_recorded", map[string]any{
		"opp_id":        opp.ID,
		"direction":     opp.Direction,
		"net_edge_bps":  opp.NetEdgeBps,
		"expected_pnl":  opp.ExpectedPnLUSD,
		"poly_price":    opp.PolyPrice,
		"kalshi_price":  opp.KalshiPrice,
	}); auditErr != nil {
		s.logger.WarnContext(ctx, "arb_service: audit log failed",
			slog.String("opp_id", opp.ID),
			slog.String("error", auditErr.Error()),
		)
	}

	s.logger.InfoContext(ctx, "arb_service: opportunity recorded",
		slog.String("opp_id", opp.ID),
		slog.Float64("net_edge_bps", opp.NetEdgeBps),
	)

	return nil
}

// MarkExecuted updates an arbitrage opportunity as executed.
func (s *ArbService) MarkExecuted(ctx context.Context, id string) error {
	if err := s.arb.MarkExecuted(ctx, id); err != nil {
		return fmt.Errorf("arb_service: mark executed %q: %w", id, err)
	}

	s.logger.InfoContext(ctx, "arb_service: opportunity marked executed",
		slog.String("opp_id", id),
	)

	return nil
}

// ListRecent returns the most recent arbitrage opportunities up to the
// specified limit.
func (s *ArbService) ListRecent(ctx context.Context, limit int) ([]domain.ArbOpportunity, error) {
	opps, err := s.arb.ListRecent(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("arb_service: list recent: %w", err)
	}
	return opps, nil
}

// ComputeRealizedPnL fills TotalFees, TotalSlippage, NetPnLUSD and per-leg
// SlippageBps on the given execution from its legs. Call after all legs are
// filled (or failed).
func (s *ArbService) ComputeRealizedPnL(exec *domain.ArbExecution) {
	var totalCost, totalRevenue, totalFees float64
	for i := range exec.Legs {
		leg := &exec.Legs[i]
		amount := leg.FilledPrice * leg.Size
		if leg.Side == domain.OrderSideBuy {
			totalCost += amount
		} else {
			totalRevenue += amount
		}
		totalFees += leg.FeeUSD
		if leg.ExpectedPrice > 0 {
			leg.SlippageBps = (leg.FilledPrice - leg.ExpectedPrice) / leg.ExpectedPrice * 10000
		}
	}
	exec.TotalFees = totalFees
	var totalSlippage float64
	for _, leg := range exec.Legs {
		totalSlippage += leg.SlippageBps
	}
	exec.TotalSlippage = totalSlippage
	exec.NetPnLUSD = totalRevenue - totalCost - totalFees
}
