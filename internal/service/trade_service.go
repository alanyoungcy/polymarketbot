package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// TradeService handles trade fill ingestion and querying.
type TradeService struct {
	trades domain.TradeStore
	bus    domain.SignalBus
	audit  domain.AuditStore
	logger *slog.Logger
}

// NewTradeService creates a TradeService with all required dependencies.
func NewTradeService(
	trades domain.TradeStore,
	bus domain.SignalBus,
	audit domain.AuditStore,
	logger *slog.Logger,
) *TradeService {
	return &TradeService{
		trades: trades,
		bus:    bus,
		audit:  audit,
		logger: logger,
	}
}

// IngestTrades inserts a batch of enriched trade fills into the store,
// publishes an event for each trade on the signal bus, and writes an
// audit log entry for the batch.
func (s *TradeService) IngestTrades(ctx context.Context, trades []domain.Trade) error {
	if len(trades) == 0 {
		return nil
	}

	if err := s.trades.InsertBatch(ctx, trades); err != nil {
		return fmt.Errorf("trade_service: insert batch: %w", err)
	}

	// Publish events for each trade.
	for _, t := range trades {
		evt, _ := json.Marshal(map[string]any{
			"event":     "trade_ingested",
			"trade_id":  t.ID,
			"market":    t.MarketID,
			"price":     t.Price,
			"amount":    t.USDAmount,
			"source":    t.Source,
			"timestamp": t.Timestamp.Format(time.RFC3339),
		})
		if pubErr := s.bus.Publish(ctx, "trades", evt); pubErr != nil {
			s.logger.WarnContext(ctx, "trade_service: publish event failed",
				slog.Int64("trade_id", t.ID),
				slog.String("error", pubErr.Error()),
			)
		}
	}

	// Audit log for the batch.
	if auditErr := s.audit.Log(ctx, "trades_ingested", map[string]any{
		"count": len(trades),
	}); auditErr != nil {
		s.logger.WarnContext(ctx, "trade_service: audit log failed",
			slog.String("error", auditErr.Error()),
		)
	}

	s.logger.InfoContext(ctx, "trade_service: ingested trades",
		slog.Int("count", len(trades)),
	)

	return nil
}

// GetLastTimestamp returns the timestamp of the most recently ingested trade,
// which is used to resume incremental ingestion.
func (s *TradeService) GetLastTimestamp(ctx context.Context) (time.Time, error) {
	ts, err := s.trades.GetLastTimestamp(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("trade_service: get last timestamp: %w", err)
	}
	return ts, nil
}

// ListByMarket returns trades for a specific market with pagination.
func (s *TradeService) ListByMarket(ctx context.Context, marketID string, opts domain.ListOpts) ([]domain.Trade, error) {
	trades, err := s.trades.ListByMarket(ctx, marketID, opts)
	if err != nil {
		return nil, fmt.Errorf("trade_service: list by market %q: %w", marketID, err)
	}
	return trades, nil
}

// ListByWallet returns trades for a specific wallet with pagination.
func (s *TradeService) ListByWallet(ctx context.Context, wallet string, opts domain.ListOpts) ([]domain.Trade, error) {
	trades, err := s.trades.ListByWallet(ctx, wallet, opts)
	if err != nil {
		return nil, fmt.Errorf("trade_service: list by wallet %q: %w", wallet, err)
	}
	return trades, nil
}
