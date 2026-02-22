package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ArbStore implements domain.ArbStore using PostgreSQL.
type ArbStore struct {
	pool *pgxpool.Pool
}

// NewArbStore creates a new ArbStore backed by the given connection pool.
func NewArbStore(pool *pgxpool.Pool) *ArbStore {
	return &ArbStore{pool: pool}
}

const arbSelectCols = `id, poly_market_id, poly_token_id, poly_price,
	kalshi_market_id, kalshi_price,
	gross_edge_bps, est_fee_bps, est_slippage_bps, est_latency_bps,
	net_edge_bps, expected_pnl_usd, direction, max_amount,
	detected_at, duration_ms, executed, executed_at`

// Insert stores a new arbitrage opportunity.
func (s *ArbStore) Insert(ctx context.Context, opp domain.ArbOpportunity) error {
	const query = `
		INSERT INTO arb_history (
			id, poly_market_id, poly_token_id, poly_price,
			kalshi_market_id, kalshi_price,
			gross_edge_bps, est_fee_bps, est_slippage_bps, est_latency_bps,
			net_edge_bps, expected_pnl_usd, direction, max_amount,
			detected_at, duration_ms, executed, executed_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14,
			$15, $16, $17, $18
		)`

	durationMs := opp.Duration.Milliseconds()

	// executed_at is only meaningful when Executed is true.
	var executedAt *time.Time
	if opp.Executed {
		now := time.Now()
		executedAt = &now
	}

	_, err := s.pool.Exec(ctx, query,
		opp.ID, opp.PolyMarketID, opp.PolyTokenID, opp.PolyPrice,
		opp.KalshiMarketID, opp.KalshiPrice,
		opp.GrossEdgeBps, opp.EstFeeBps, opp.EstSlippageBps, opp.EstLatencyBps,
		opp.NetEdgeBps, opp.ExpectedPnLUSD, opp.Direction, opp.MaxAmount,
		opp.DetectedAt, durationMs, opp.Executed, executedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert arb opportunity %s: %w", opp.ID, err)
	}
	return nil
}

// MarkExecuted sets the executed flag and executed_at timestamp for a given opportunity.
func (s *ArbStore) MarkExecuted(ctx context.Context, id string) error {
	const query = `
		UPDATE arb_history SET
			executed    = TRUE,
			executed_at = NOW()
		WHERE id = $1`

	tag, err := s.pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("postgres: mark arb executed %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListRecent returns the most recent arbitrage opportunities ordered by detection time.
func (s *ArbStore) ListRecent(ctx context.Context, limit int) ([]domain.ArbOpportunity, error) {
	query := `SELECT ` + arbSelectCols + ` FROM arb_history ORDER BY detected_at DESC`
	args := []any{}

	if limit > 0 {
		query += " LIMIT $1"
		args = append(args, limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list recent arbs: %w", err)
	}
	defer rows.Close()

	var opps []domain.ArbOpportunity
	for rows.Next() {
		var opp domain.ArbOpportunity
		var durationMs int64
		var executedAt *time.Time

		if err := rows.Scan(
			&opp.ID, &opp.PolyMarketID, &opp.PolyTokenID, &opp.PolyPrice,
			&opp.KalshiMarketID, &opp.KalshiPrice,
			&opp.GrossEdgeBps, &opp.EstFeeBps, &opp.EstSlippageBps, &opp.EstLatencyBps,
			&opp.NetEdgeBps, &opp.ExpectedPnLUSD, &opp.Direction, &opp.MaxAmount,
			&opp.DetectedAt, &durationMs, &opp.Executed, &executedAt,
		); err != nil {
			return nil, fmt.Errorf("postgres: scan arb: %w", err)
		}
		opp.Duration = time.Duration(durationMs) * time.Millisecond
		opps = append(opps, opp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list recent arbs rows: %w", err)
	}
	return opps, nil
}
