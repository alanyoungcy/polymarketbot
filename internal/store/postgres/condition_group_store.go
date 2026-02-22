package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alanyoungcy/polymarketbot/internal/domain"
)

// ConditionGroupStore implements domain.ConditionGroupStore using PostgreSQL.
type ConditionGroupStore struct {
	pool *pgxpool.Pool
}

// NewConditionGroupStore creates a new ConditionGroupStore.
func NewConditionGroupStore(pool *pgxpool.Pool) *ConditionGroupStore {
	return &ConditionGroupStore{pool: pool}
}

// Upsert inserts or updates a condition group.
func (s *ConditionGroupStore) Upsert(ctx context.Context, g domain.ConditionGroup) error {
	const query = `
		INSERT INTO condition_groups (id, title, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			title = EXCLUDED.title,
			status = EXCLUDED.status,
			updated_at = NOW()`
	_, err := s.pool.Exec(ctx, query, g.ID, g.Title, g.Status, g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: upsert condition_group %s: %w", g.ID, err)
	}
	return nil
}

// GetByID returns a condition group by id.
func (s *ConditionGroupStore) GetByID(ctx context.Context, id string) (domain.ConditionGroup, error) {
	const query = `SELECT id, title, status, created_at, updated_at FROM condition_groups WHERE id = $1`
	var g domain.ConditionGroup
	err := s.pool.QueryRow(ctx, query, id).Scan(&g.ID, &g.Title, &g.Status, &g.CreatedAt, &g.UpdatedAt)
	if err != nil {
		return domain.ConditionGroup{}, fmt.Errorf("postgres: get condition_group %s: %w", id, err)
	}
	return g, nil
}

// ListMarkets returns market IDs linked to the group.
func (s *ConditionGroupStore) ListMarkets(ctx context.Context, groupID string) ([]string, error) {
	const query = `SELECT market_id FROM condition_group_markets WHERE group_id = $1 ORDER BY market_id`
	rows, err := s.pool.Query(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("postgres: list markets for group %s: %w", groupID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// LinkMarket links a market to a condition group.
func (s *ConditionGroupStore) LinkMarket(ctx context.Context, groupID, marketID string) error {
	const query = `INSERT INTO condition_group_markets (group_id, market_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`
	_, err := s.pool.Exec(ctx, query, groupID, marketID)
	if err != nil {
		return fmt.Errorf("postgres: link market %s to group %s: %w", marketID, groupID, err)
	}
	return nil
}

// List returns all condition groups.
func (s *ConditionGroupStore) List(ctx context.Context) ([]domain.ConditionGroup, error) {
	const query = `SELECT id, title, status, created_at, updated_at FROM condition_groups ORDER BY id`
	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("postgres: list condition_groups: %w", err)
	}
	defer rows.Close()
	var list []domain.ConditionGroup
	for rows.Next() {
		var g domain.ConditionGroup
		if err := rows.Scan(&g.ID, &g.Title, &g.Status, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		list = append(list, g)
	}
	return list, rows.Err()
}
