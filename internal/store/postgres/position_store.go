package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// PositionStore implements domain.PositionStore using PostgreSQL.
type PositionStore struct {
	pool *pgxpool.Pool
}

// NewPositionStore creates a new PositionStore backed by the given connection pool.
func NewPositionStore(pool *pgxpool.Pool) *PositionStore {
	return &PositionStore{pool: pool}
}

const positionSelectCols = `id, market_id, token_id, wallet, side, direction,
	entry_price, size, take_profit, stop_loss, realized_pnl,
	status, strategy_name, opened_at, closed_at, exit_price`

func scanPositionRow(row pgx.Row) (domain.Position, error) {
	var p domain.Position
	var direction, status string

	err := row.Scan(
		&p.ID, &p.MarketID, &p.TokenID, &p.Wallet,
		&p.Side, &direction,
		&p.EntryPrice, &p.Size,
		&p.TakeProfit, &p.StopLoss, &p.RealizedPnL,
		&status, &p.Strategy,
		&p.OpenedAt, &p.ClosedAt, &p.ExitPrice,
	)
	if err != nil {
		return domain.Position{}, err
	}
	p.Direction = domain.OrderSide(direction)
	p.Status = domain.PositionStatus(status)
	return p, nil
}

func scanPositionRows(rows pgx.Rows) ([]domain.Position, error) {
	var positions []domain.Position
	for rows.Next() {
		var p domain.Position
		var direction, status string

		if err := rows.Scan(
			&p.ID, &p.MarketID, &p.TokenID, &p.Wallet,
			&p.Side, &direction,
			&p.EntryPrice, &p.Size,
			&p.TakeProfit, &p.StopLoss, &p.RealizedPnL,
			&status, &p.Strategy,
			&p.OpenedAt, &p.ClosedAt, &p.ExitPrice,
		); err != nil {
			return nil, err
		}
		p.Direction = domain.OrderSide(direction)
		p.Status = domain.PositionStatus(status)
		positions = append(positions, p)
	}
	return positions, rows.Err()
}

// Create inserts a new position.
func (s *PositionStore) Create(ctx context.Context, p domain.Position) error {
	const query = `
		INSERT INTO positions (
			id, market_id, token_id, wallet, side, direction,
			entry_price, size, take_profit, stop_loss, realized_pnl,
			status, strategy_name, opened_at, closed_at, exit_price, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14, $15, $16, NOW()
		)`

	_, err := s.pool.Exec(ctx, query,
		p.ID, p.MarketID, p.TokenID, p.Wallet,
		p.Side, string(p.Direction),
		p.EntryPrice, p.Size,
		p.TakeProfit, p.StopLoss, p.RealizedPnL,
		string(p.Status), p.Strategy,
		p.OpenedAt, p.ClosedAt, p.ExitPrice,
	)
	if err != nil {
		return fmt.Errorf("postgres: create position %s: %w", p.ID, err)
	}
	return nil
}

// Update replaces all mutable fields of a position.
func (s *PositionStore) Update(ctx context.Context, p domain.Position) error {
	const query = `
		UPDATE positions SET
			market_id     = $2,
			token_id      = $3,
			wallet        = $4,
			side          = $5,
			direction     = $6,
			entry_price   = $7,
			size          = $8,
			take_profit   = $9,
			stop_loss     = $10,
			realized_pnl  = $11,
			status        = $12,
			strategy_name = $13,
			closed_at     = $14,
			exit_price    = $15,
			updated_at    = NOW()
		WHERE id = $1`

	tag, err := s.pool.Exec(ctx, query,
		p.ID, p.MarketID, p.TokenID, p.Wallet,
		p.Side, string(p.Direction),
		p.EntryPrice, p.Size,
		p.TakeProfit, p.StopLoss, p.RealizedPnL,
		string(p.Status), p.Strategy,
		p.ClosedAt, p.ExitPrice,
	)
	if err != nil {
		return fmt.Errorf("postgres: update position %s: %w", p.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Close marks a position as closed, setting the exit price and closed_at timestamp.
func (s *PositionStore) Close(ctx context.Context, id string, exitPrice float64) error {
	const query = `
		UPDATE positions SET
			status     = 'closed',
			exit_price = $2,
			closed_at  = NOW(),
			updated_at = NOW()
		WHERE id = $1 AND status = 'open'`

	tag, err := s.pool.Exec(ctx, query, id, exitPrice)
	if err != nil {
		return fmt.Errorf("postgres: close position %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// GetOpen returns all open positions for the given wallet.
func (s *PositionStore) GetOpen(ctx context.Context, wallet string) ([]domain.Position, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+positionSelectCols+` FROM positions
		 WHERE wallet = $1 AND status = 'open'
		 ORDER BY opened_at DESC`, wallet)
	if err != nil {
		return nil, fmt.Errorf("postgres: get open positions: %w", err)
	}
	defer rows.Close()

	positions, err := scanPositionRows(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan open positions: %w", err)
	}
	return positions, nil
}

// GetByID retrieves a single position by its ID.
func (s *PositionStore) GetByID(ctx context.Context, id string) (domain.Position, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+positionSelectCols+` FROM positions WHERE id = $1`, id)

	p, err := scanPositionRow(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return domain.Position{}, domain.ErrNotFound
		}
		return domain.Position{}, fmt.Errorf("postgres: get position %s: %w", id, err)
	}
	return p, nil
}

// ListHistory returns positions for the given wallet with pagination and optional time filtering.
func (s *PositionStore) ListHistory(ctx context.Context, wallet string, opts domain.ListOpts) ([]domain.Position, error) {
	query := `SELECT ` + positionSelectCols + ` FROM positions WHERE wallet = $1`
	args := []any{wallet}
	argIdx := 2

	if opts.Since != nil {
		query += fmt.Sprintf(" AND opened_at >= $%d", argIdx)
		args = append(args, *opts.Since)
		argIdx++
	}
	if opts.Until != nil {
		query += fmt.Sprintf(" AND opened_at <= $%d", argIdx)
		args = append(args, *opts.Until)
		argIdx++
	}

	query += " ORDER BY opened_at DESC"

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, opts.Limit)
		argIdx++
	}
	if opts.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, opts.Offset)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: list position history: %w", err)
	}
	defer rows.Close()

	positions, err := scanPositionRows(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan position history: %w", err)
	}
	return positions, nil
}
