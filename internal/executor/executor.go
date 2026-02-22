package executor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
	"github.com/alanyoungcy/polymarketbot/internal/service"
)

// OrderPlacer is the interface through which the executor submits orders to the
// exchange. It is typically implemented by the service layer.
type OrderPlacer interface {
	PlaceOrder(ctx context.Context, sig domain.TradeSignal) (domain.OrderResult, error)
}

// ReplaceOrderer is optional. When implemented, the executor uses ReplaceOrder
// for liquidity_provider requotes (cancel existing order and place new one).
type ReplaceOrderer interface {
	ReplaceOrder(ctx context.Context, cancelID string, newSig domain.TradeSignal) (domain.OrderResult, error)
}

// RiskChecker validates whether a trade signal passes pre-trade risk controls
// (e.g., position limits, drawdown checks, margin requirements).
type RiskChecker interface {
	PreTradeCheck(ctx context.Context, signal domain.TradeSignal, wallet string) error
}

// Executor reads trade signals from a channel, applies deduplication, expiry,
// and risk checks, then places orders through the OrderPlacer interface.
// When signals have leg_group_id in metadata they are buffered and executed
// as a group; if ArbService and ArbExecutionStore are set, executions are recorded.
type Executor struct {
	signalCh <-chan domain.TradeSignal
	orderSvc OrderPlacer
	riskSvc  RiskChecker
	dedup    *Dedup
	wallet   string
	logger   *slog.Logger

	legAccum   *LegGroupAccumulator
	arbSvc     *service.ArbService
	arbExecStore domain.ArbExecutionStore
	maxLegGapMs  int64

	cleanupInterval time.Duration

	// lastLPOrderID tracks the last order ID per (tokenID, side) for liquidity_provider requotes.
	lastLPOrderID   map[string]string
	lastLPOrderIDMu sync.Mutex
}

// NewExecutor creates an Executor that reads signals from signalCh, validates
// them through riskSvc, and places orders via orderSvc. The wallet string
// identifies the trading wallet for risk checks.
func NewExecutor(
	signalCh <-chan domain.TradeSignal,
	orderSvc OrderPlacer,
	riskSvc RiskChecker,
	wallet string,
	logger *slog.Logger,
) *Executor {
	return &Executor{
		signalCh:        signalCh,
		orderSvc:        orderSvc,
		riskSvc:         riskSvc,
		dedup:           NewDedup(2 * time.Minute),
		wallet:          wallet,
		logger:          logger.With(slog.String("component", "executor")),
		cleanupInterval: 30 * time.Second,
		maxLegGapMs:     2000,
		lastLPOrderID:   make(map[string]string),
	}
}

// SetArbRecording enables multi-leg accumulation and arb execution recording.
// maxLegGapMs is the max time allowed between first and last leg in a group.
func (e *Executor) SetArbRecording(arbSvc *service.ArbService, arbExecStore domain.ArbExecutionStore, maxLegGapMs int64) {
	e.arbSvc = arbSvc
	e.arbExecStore = arbExecStore
	if maxLegGapMs > 0 {
		e.maxLegGapMs = maxLegGapMs
	}
	e.legAccum = NewLegGroupAccumulator(e.maxLegGapMs, e.placeLegGroup, e.logger)
}

// placeLegGroup is the onComplete callback: place each leg, then record execution.
func (e *Executor) placeLegGroup(ctx context.Context, legs []domain.TradeSignal, policy domain.LegPolicy) error {
	results := make([]domain.OrderResult, 0, len(legs))
	for _, sig := range legs {
		res, err := e.orderSvc.PlaceOrder(ctx, sig)
		if err != nil {
			e.logger.Error("leg group place order failed", slog.String("signal_id", sig.ID), slog.String("error", err.Error()))
			res = domain.OrderResult{Success: false, OrderID: "", Status: domain.OrderStatusFailed}
		}
		results = append(results, res)
		if policy == domain.LegPolicyAllOrNone && !res.Success {
			e.logger.Warn("all_or_none: leg failed, stopping", slog.String("signal_id", sig.ID))
			break
		}
	}
	if e.arbSvc == nil || e.arbExecStore == nil {
		return nil
	}
	oppID := ""
	if len(legs) > 0 && legs[0].Metadata != nil {
		oppID = legs[0].Metadata["opp_id"]
	}
	arbType := domain.ArbTypeRebalancing
	if len(legs) > 0 && legs[0].Metadata != nil {
		if t := legs[0].Metadata["arb_type"]; t != "" {
			arbType = domain.ArbType(t)
		}
	}
	legGroupID := ""
	if len(legs) > 0 && legs[0].Metadata != nil {
		legGroupID = legs[0].Metadata["leg_group_id"]
	}
	exec := domain.ArbExecution{
		ID:            uuid.New().String(),
		OpportunityID: oppID,
		ArbType:       arbType,
		LegGroupID:    legGroupID,
		Legs:          make([]domain.ArbLeg, 0, len(legs)),
		Status:        domain.ArbExecFilled,
		StartedAt:     time.Now().UTC(),
	}
	now := time.Now().UTC()
	exec.CompletedAt = &now
	for i, sig := range legs {
		res := domain.OrderResult{}
		if i < len(results) {
			res = results[i]
		}
		leg := domain.ArbLeg{
			OrderID:       res.OrderID,
			MarketID:      sig.MarketID,
			TokenID:       sig.TokenID,
			Side:          sig.Side,
			ExpectedPrice: sig.Price(),
			FilledPrice:   res.FilledPrice,
			Size:          sig.Size(),
			FeeUSD:        res.FeeUSD,
			Status:        res.Status,
		}
		if leg.ExpectedPrice > 0 {
			leg.SlippageBps = (leg.FilledPrice - leg.ExpectedPrice) / leg.ExpectedPrice * 10000
		}
		exec.Legs = append(exec.Legs, leg)
	}
	e.arbSvc.ComputeRealizedPnL(&exec)
	if err := e.arbExecStore.Create(ctx, exec); err != nil {
		e.logger.Warn("arb execution record failed", slog.String("error", err.Error()))
	}
	return nil
}

// Run starts the executor's main loop. It processes signals until the context
// is cancelled, at which point it drains any remaining signals in the channel
// and returns.
func (e *Executor) Run(ctx context.Context) error {
	e.logger.Info("executor started")
	defer e.logger.Info("executor stopped")

	cleanupTicker := time.NewTicker(e.cleanupInterval)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			e.drain()
			return ctx.Err()

		case sig, ok := <-e.signalCh:
			if !ok {
				// Channel closed; shut down.
				return nil
			}
			e.process(ctx, sig)

		case <-cleanupTicker.C:
			e.dedup.Cleanup()
		}
	}
}

// process handles a single trade signal through the full validation and
// execution pipeline.
func (e *Executor) process(ctx context.Context, sig domain.TradeSignal) {
	log := e.logger.With(
		slog.String("signal_id", sig.ID),
		slog.String("source", sig.Source),
		slog.String("token", sig.TokenID),
		slog.String("side", string(sig.Side)),
	)

	// 0. Multi-leg: buffer and run group when complete.
	if e.legAccum != nil && sig.Metadata != nil && sig.Metadata["leg_group_id"] != "" {
		if e.legAccum.Add(ctx, sig) {
			return
		}
	}

	// 1. Deduplication.
	if e.dedup.IsDuplicate(sig.ID) {
		log.Debug("signal deduplicated, skipping")
		return
	}

	// 2. Expiry check.
	if !sig.ExpiresAt.IsZero() && time.Now().UTC().After(sig.ExpiresAt) {
		log.Warn("signal expired, skipping",
			slog.Time("expires_at", sig.ExpiresAt),
		)
		return
	}

	// 3. Pre-trade risk check.
	if err := e.riskSvc.PreTradeCheck(ctx, sig, e.wallet); err != nil {
		log.Warn("risk check failed, skipping",
			slog.String("error", err.Error()),
		)
		return
	}

	// 4. Place or replace order (LP requote: replace when we have a previous order for same token+side).
	var result domain.OrderResult
	var err error
	didReplace := false
	if sig.Source == "liquidity_provider" {
		e.lastLPOrderIDMu.Lock()
		key := "lp:" + sig.TokenID + ":" + string(sig.Side)
		prevID := e.lastLPOrderID[key]
		e.lastLPOrderIDMu.Unlock()
		if prevID != "" {
			if repl, ok := e.orderSvc.(ReplaceOrderer); ok {
				result, err = repl.ReplaceOrder(ctx, prevID, sig)
				didReplace = true
			}
		}
	}
	if !didReplace || err != nil {
		result, err = e.orderSvc.PlaceOrder(ctx, sig)
	}
	if err == nil && result.Success && sig.Source == "liquidity_provider" {
		e.lastLPOrderIDMu.Lock()
		e.lastLPOrderID["lp:"+sig.TokenID+":"+string(sig.Side)] = result.OrderID
		e.lastLPOrderIDMu.Unlock()
	}

	if err != nil {
		log.Error("order placement failed",
			slog.String("error", err.Error()),
		)
		return
	}

	if !result.Success {
		log.Warn("order rejected",
			slog.String("order_id", result.OrderID),
			slog.String("status", string(result.Status)),
			slog.String("message", result.Message),
			slog.Bool("should_retry", result.ShouldRetry),
		)
		if result.ShouldRetry {
			e.retryOrder(ctx, sig, log)
		}
		return
	}

	log.Info("order placed successfully",
		slog.String("order_id", result.OrderID),
		slog.String("status", string(result.Status)),
	)
}

// retryOrder makes a single retry attempt for a failed order. A production
// system would use exponential back-off and a bounded retry count; this
// implementation performs one retry after a short pause.
func (e *Executor) retryOrder(ctx context.Context, sig domain.TradeSignal, log *slog.Logger) {
	// Respect expiry even for retries.
	if !sig.ExpiresAt.IsZero() && time.Now().UTC().After(sig.ExpiresAt) {
		log.Warn("signal expired during retry, giving up")
		return
	}

	select {
	case <-ctx.Done():
		return
	case <-time.After(500 * time.Millisecond):
	}

	result, err := e.orderSvc.PlaceOrder(ctx, sig)
	if err != nil {
		log.Error("retry order placement failed",
			slog.String("error", err.Error()),
		)
		return
	}

	if result.Success {
		log.Info("retry order placed successfully",
			slog.String("order_id", result.OrderID),
		)
	} else {
		log.Warn("retry order also rejected",
			slog.String("message", result.Message),
		)
	}
}

// drain processes any signals already buffered in the channel after context
// cancellation. This ensures in-flight signals are not silently dropped.
func (e *Executor) drain() {
	for {
		select {
		case sig, ok := <-e.signalCh:
			if !ok {
				return
			}
			e.logger.Warn("draining signal after shutdown",
				slog.String("signal_id", sig.ID),
			)
			// We use a short-lived context for draining so we don't hang
			// indefinitely on external calls during shutdown.
			drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			e.process(drainCtx, sig)
			cancel()
		default:
			return
		}
	}
}

// SetDedupTTL replaces the dedup instance with a new one using the given TTL.
// This is useful for testing or runtime reconfiguration.
func (e *Executor) SetDedupTTL(ttl time.Duration) {
	e.dedup = NewDedup(ttl)
}

// SetCleanupInterval changes how often the dedup map is garbage-collected.
// Must be called before Run.
func (e *Executor) SetCleanupInterval(d time.Duration) {
	e.cleanupInterval = d
}

// Wallet returns the wallet address this executor is configured with.
func (e *Executor) Wallet() string {
	return e.wallet
}

var _ fmt.Stringer = (*Executor)(nil)

// String returns a human-readable description of the executor.
func (e *Executor) String() string {
	return fmt.Sprintf("Executor(wallet=%s)", e.wallet)
}
