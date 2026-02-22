package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// BondPositionStore implements domain.BondPositionStore using PostgreSQL.
type BondPositionStore struct {
	pool *pgxpool.Pool
}

// NewBondPositionStore creates a new BondPositionStore.
func NewBondPositionStore(pool *pgxpool.Pool) *BondPositionStore {
	return &BondPositionStore{pool: pool}
}

// Create inserts a new bond position.
func (s *BondPositionStore) Create(ctx context.Context, pos domain.BondPosition) error {
	const query = `
		INSERT INTO bond_positions (id, market_id, token_id, entry_price, expected_expiry, expected_apr, size, status, realized_pnl, created_at, resolved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	_, err := s.pool.Exec(ctx, query,
		pos.ID, pos.MarketID, pos.TokenID, pos.EntryPrice, pos.ExpectedExpiry, pos.ExpectedAPR,
		pos.Size, string(pos.Status), pos.RealizedPnL, pos.CreatedAt, pos.ResolvedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: create bond_position %s: %w", pos.ID, err)
	}
	return nil
}

// Update updates an existing bond position.
func (s *BondPositionStore) Update(ctx context.Context, pos domain.BondPosition) error {
	const query = `
		UPDATE bond_positions SET
			status = $2, realized_pnl = $3, resolved_at = $4
		WHERE id = $1`
	_, err := s.pool.Exec(ctx, query, pos.ID, string(pos.Status), pos.RealizedPnL, pos.ResolvedAt)
	if err != nil {
		return fmt.Errorf("postgres: update bond_position %s: %w", pos.ID, err)
	}
	return nil
}

// GetOpen returns all open bond positions.
func (s *BondPositionStore) GetOpen(ctx context.Context) ([]domain.BondPosition, error) {
	const query = `
		SELECT id, market_id, token_id, entry_price, expected_expiry, expected_apr, size, status, realized_pnl, created_at, resolved_at
		FROM bond_positions WHERE status = 'open' ORDER BY expected_expiry`
	return s.queryPositions(ctx, query)
}

// GetByID returns a bond position by id.
func (s *BondPositionStore) GetByID(ctx context.Context, id string) (domain.BondPosition, error) {
	const query = `
		SELECT id, market_id, token_id, entry_price, expected_expiry, expected_apr, size, status, realized_pnl, created_at, resolved_at
		FROM bond_positions WHERE id = $1`
	var pos domain.BondPosition
	var status string
	err := s.pool.QueryRow(ctx, query, id).Scan(
		&pos.ID, &pos.MarketID, &pos.TokenID, &pos.EntryPrice, &pos.ExpectedExpiry, &pos.ExpectedAPR,
		&pos.Size, &status, &pos.RealizedPnL, &pos.CreatedAt, &pos.ResolvedAt,
	)
	if err != nil {
		return domain.BondPosition{}, fmt.Errorf("postgres: get bond_position %s: %w", id, err)
	}
	pos.Status = domain.BondStatus(status)
	return pos, nil
}

func (s *BondPositionStore) queryPositions(ctx context.Context, query string, args ...any) ([]domain.BondPosition, error) {
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []domain.BondPosition
	for rows.Next() {
		var pos domain.BondPosition
		var status string
		if err := rows.Scan(
			&pos.ID, &pos.MarketID, &pos.TokenID, &pos.EntryPrice, &pos.ExpectedExpiry, &pos.ExpectedAPR,
			&pos.Size, &status, &pos.RealizedPnL, &pos.CreatedAt, &pos.ResolvedAt,
		); err != nil {
			return nil, err
		}
		pos.Status = domain.BondStatus(status)
		list = append(list, pos)
	}
	return list, rows.Err()
}
