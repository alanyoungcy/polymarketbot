package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ArbExecutionStore implements domain.ArbExecutionStore using PostgreSQL.
type ArbExecutionStore struct {
	pool *pgxpool.Pool
}

// NewArbExecutionStore creates a new ArbExecutionStore.
func NewArbExecutionStore(pool *pgxpool.Pool) *ArbExecutionStore {
	return &ArbExecutionStore{pool: pool}
}

// Create inserts an arb execution and its legs.
func (s *ArbExecutionStore) Create(ctx context.Context, exec domain.ArbExecution) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var completedAt *time.Time
	if exec.CompletedAt != nil {
		completedAt = exec.CompletedAt
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO arb_executions (id, opportunity_id, arb_type, leg_group_id, gross_edge_bps, total_fees, total_slippage, net_pnl_usd, status, started_at, completed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		exec.ID, exec.OpportunityID, string(exec.ArbType), exec.LegGroupID,
		exec.GrossEdgeBps, exec.TotalFees, exec.TotalSlippage, exec.NetPnLUSD,
		string(exec.Status), exec.StartedAt, completedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: insert arb_execution: %w", err)
	}

	for _, leg := range exec.Legs {
		_, err = tx.Exec(ctx, `
			INSERT INTO arb_execution_legs (execution_id, order_id, market_id, token_id, side, expected_price, filled_price, size, fee_usd, slippage_bps, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			exec.ID, leg.OrderID, leg.MarketID, leg.TokenID, string(leg.Side),
			leg.ExpectedPrice, leg.FilledPrice, leg.Size, leg.FeeUSD, leg.SlippageBps, string(leg.Status),
		)
		if err != nil {
			return fmt.Errorf("postgres: insert arb_execution_leg: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetByID returns an execution with its legs.
func (s *ArbExecutionStore) GetByID(ctx context.Context, id string) (domain.ArbExecution, error) {
	var exec domain.ArbExecution
	var completedAt *time.Time
	var arbType, statusStr string
	err := s.pool.QueryRow(ctx, `
		SELECT id, opportunity_id, arb_type, leg_group_id, gross_edge_bps, total_fees, total_slippage, net_pnl_usd, status, started_at, completed_at
		FROM arb_executions WHERE id = $1`,
		id,
	).Scan(&exec.ID, &exec.OpportunityID, &arbType, &exec.LegGroupID,
		&exec.GrossEdgeBps, &exec.TotalFees, &exec.TotalSlippage, &exec.NetPnLUSD,
		&statusStr, &exec.StartedAt, &completedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ArbExecution{}, domain.ErrNotFound
		}
		return domain.ArbExecution{}, fmt.Errorf("postgres: get arb_execution %s: %w", id, err)
	}
	exec.ArbType = domain.ArbType(arbType)
	exec.Status = domain.ArbExecStatus(statusStr)
	exec.CompletedAt = completedAt

	rows, err := s.pool.Query(ctx, `
		SELECT order_id, market_id, token_id, side, expected_price, filled_price, size, fee_usd, slippage_bps, status
		FROM arb_execution_legs WHERE execution_id = $1 ORDER BY id`,
		id,
	)
	if err != nil {
		return domain.ArbExecution{}, fmt.Errorf("postgres: get arb_execution_legs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var leg domain.ArbLeg
		var side, status string
		if err := rows.Scan(&leg.OrderID, &leg.MarketID, &leg.TokenID, &side, &leg.ExpectedPrice, &leg.FilledPrice, &leg.Size, &leg.FeeUSD, &leg.SlippageBps, &status); err != nil {
			return domain.ArbExecution{}, err
		}
		leg.Side = domain.OrderSide(side)
		leg.Status = domain.OrderStatus(status)
		exec.Legs = append(exec.Legs, leg)
	}
	if err := rows.Err(); err != nil {
		return domain.ArbExecution{}, err
	}
	return exec, nil
}

// ListRecent returns the most recent executions.
func (s *ArbExecutionStore) ListRecent(ctx context.Context, limit int) ([]domain.ArbExecution, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, opportunity_id, arb_type, leg_group_id, gross_edge_bps, total_fees, total_slippage, net_pnl_usd, status, started_at, completed_at
		FROM arb_executions ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: list arb_executions: %w", err)
	}
	defer rows.Close()
	var list []domain.ArbExecution
	for rows.Next() {
		var exec domain.ArbExecution
		var completedAt *time.Time
		var arbType, statusStr string
		if err := rows.Scan(&exec.ID, &exec.OpportunityID, &arbType, &exec.LegGroupID,
			&exec.GrossEdgeBps, &exec.TotalFees, &exec.TotalSlippage, &exec.NetPnLUSD,
			&statusStr, &exec.StartedAt, &completedAt); err != nil {
			return nil, err
		}
		exec.ArbType = domain.ArbType(arbType)
		exec.Status = domain.ArbExecStatus(statusStr)
		exec.CompletedAt = completedAt
		list = append(list, exec)
	}
	return list, rows.Err()
}

// SumPnL returns the sum of net_pnl_usd for executions since the given time.
func (s *ArbExecutionStore) SumPnL(ctx context.Context, since time.Time) (float64, error) {
	var sum float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(net_pnl_usd), 0) FROM arb_executions WHERE started_at >= $1`, since).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("postgres: sum arb_executions pnl: %w", err)
	}
	return sum, nil
}

// SumPnLByType returns the sum of net_pnl_usd for the given arb type since the given time.
func (s *ArbExecutionStore) SumPnLByType(ctx context.Context, arbType domain.ArbType, since time.Time) (float64, error) {
	var sum float64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(net_pnl_usd), 0) FROM arb_executions WHERE arb_type = $1 AND started_at >= $2`, string(arbType), since).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("postgres: sum arb_executions pnl by type: %w", err)
	}
	return sum, nil
}
